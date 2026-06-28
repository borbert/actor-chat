 use std::collections::HashSet;

  use axum::extract::ws::{Message, WebSocket};
  use futures_util::{SinkExt, StreamExt};
  use serde_json::json;
  use tokio::sync::mpsc;

  use crate::actors::{ConnId, RoomId, UserId};
  use crate::convex::SentMessage;
  use crate::protocol::{Inbound, Outbound};
  use crate::AppState;

  /// Authenticated identity for a connection, established at the WS upgrade.
  pub struct Identity {
      pub user: UserId,    // Convex users._id
      pub auth_id: String, // Clerk subject (sub); guards token refreshes
      pub token: String,   // current Clerk JWT lease (refreshed via ping)
  }

  /// Address of a connection actor: enqueue an Outbound frame for this client.
  #[derive(Clone)]
  pub struct ConnHandle {
      pub id: ConnId,
      tx: mpsc::Sender<Outbound>,
  }

  impl ConnHandle {
      pub async fn send(&self, frame: Outbound) {
          let _ = self.tx.send(frame).await;
      }
  }

  /// Drive one WebSocket connection: writer actor + read loop.
  pub async fn run(socket: WebSocket, state: AppState, identity: Identity) {
      let id = ConnId::next();
      let (mut sink, mut stream) = socket.split();

      // ---- connection actor: owns the write half, drains its mailbox ----
      let (out_tx, mut out_rx) = mpsc::channel::<Outbound>(64);
      let writer = tokio::spawn(async move {
          while let Some(frame) = out_rx.recv().await {
              let text = serde_json::to_string(&frame).expect("serialize outbound");
              if sink.send(Message::Text(text.into())).await.is_err() {
                  break;
              }
          }
      });

      let conn = ConnHandle { id, tx: out_tx };

      conn.send(Outbound::Hello {
          server: "rust-actor-chat".into(),
          version: env!("CARGO_PKG_VERSION").into(),
      })
      .await;

      let user = identity.user;
      let mut token = identity.token; // mutable lease, refreshed on ping
      let mut joined: HashSet<RoomId> = HashSet::new();

      while let Some(Ok(msg)) = stream.next().await {
          let text = match msg {
              Message::Text(t) => t,
              Message::Close(_) => break,
              _ => continue,
          };

          let frame = match serde_json::from_str::<Inbound>(&text) {
              Ok(f) => f,
              Err(e) => {
                  conn.send(Outbound::Error {
                      room_id: None,
                      client_id: None,
                      reason: format!("bad frame: {e}"),
                  })
                  .await;
                  continue;
              }
          };

          match frame {
              Inbound::Join { room_id } => {
                  let rid = RoomId(room_id);
                  if joined.insert(rid.clone()) {
                      state.registry.handle_for(&rid).join(conn.clone(), user.clone()).await;
                  }
              }
              Inbound::Leave { room_id } => {
                  let rid = RoomId(room_id);
                  if joined.remove(&rid) {
                      state.registry.handle_for(&rid).leave(conn.id).await;
                  }
              }
              Inbound::TypingStart { room_id } => {
                  let rid = RoomId(room_id);
                  if joined.contains(&rid) {
                      state.registry.handle_for(&rid).typing(conn.id, user.clone(), true).await;
                  }
              }
              Inbound::TypingStop { room_id } => {
                  let rid = RoomId(room_id);
                  if joined.contains(&rid) {
                      state.registry.handle_for(&rid).typing(conn.id, user.clone(), false).await;
                  }
              }
              Inbound::Send { room_id, body, client_id, .. } => {
                  // Persist off the read loop so a slow DB write can't stall reads.
                  // Convex's by_room_clientId index is the idempotency authority.
                  let convex = state.convex.with_auth(&token);
                  let conn = conn.clone();
                  tokio::spawn(async move {
                      let result = convex
                          .mutation::<_, SentMessage>(
                              "messages:send",
                              json!({
                                  "roomId": room_id.clone(),
                                  "body": body,
                                  "clientId": client_id.clone(),
                              }),
                          )
                          .await;
                      let frame = match result {
                          Ok(m) => Outbound::Ack {
                              room_id,
                              message_id: m.id,
                              client_id,
                              created_at: m.created_at,
                          },
                          Err(e) => Outbound::Error {
                              room_id: Some(room_id),
                              client_id: Some(client_id),
                              reason: e.to_string(),
                          },
                      };
                      conn.send(frame).await;
                  });
              }
              Inbound::Ping { token: refreshed } => {
                  // Token-lease refresh: swap in a fresh token if valid and same subject.
                  if let Some(fresh) = refreshed {
                      match state.validator.validate(&fresh).await {
                          Ok(sub) if sub == identity.auth_id => token = fresh,
                          Ok(_) => tracing::warn!("ping token subject mismatch; keeping current token"),
                          Err(e) => tracing::warn!("ping token invalid: {e}"),
                      }
                  }
                  conn.send(Outbound::Pong).await;
              }
          }
      }

      for rid in joined.drain() {
          state.registry.handle_for(&rid).leave(conn.id).await;
      }
      writer.abort();
  }
