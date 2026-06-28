use std::collections::HashMap;
  use std::time::{Duration, Instant};

  use tokio::sync::mpsc;
  use tokio::time::interval;

  use crate::actors::connection::ConnHandle;
  use crate::actors::{ConnId, RoomId, UserId};
  use crate::protocol::Outbound;

  /// How often an idle room checks whether to evict itself.
  const IDLE_CHECK: Duration = Duration::from_secs(60);
  /// How long a room must stay empty before self-eviction.
  const IDLE_TIMEOUT: Duration = Duration::from_secs(300);

  struct Member {
      conn: ConnHandle,
      user: UserId,
  }

  pub enum RoomMsg {
      Join { conn: ConnHandle, user: UserId },
      Leave { conn: ConnId },
      Typing { conn: ConnId, user: UserId, on: bool },
  }

  struct RoomActor {
      room_id: RoomId,
      rx: mpsc::Receiver<RoomMsg>,
      members: HashMap<ConnId, Member>,
  }

  impl RoomActor {
      async fn run(mut self) {
          let mut ticker = interval(IDLE_CHECK);
          ticker.tick().await; // discard the immediate first tick
          let mut last_activity = Instant::now();

          loop {
              tokio::select! {
                  maybe = self.rx.recv() => {
                      let Some(msg) = maybe else { break }; // all handles dropped
                      last_activity = Instant::now();
                      match msg {
                          RoomMsg::Join { conn, user } => {
                              self.members.insert(conn.id, Member { conn, user });
                              self.broadcast_presence().await;
                          }
                          RoomMsg::Leave { conn } => {
                              if self.members.remove(&conn).is_some() {
                                  self.broadcast_presence().await;
                              }
                          }
                          RoomMsg::Typing { conn, user, on } => self.on_typing(conn, user, on).await,
                      }
                  }
                  _ = ticker.tick() => {
                      if self.members.is_empty() && last_activity.elapsed() >= IDLE_TIMEOUT {
                          tracing::info!(room = %self.room_id.0, "room evicted (idle)");
                          break;
                      }
                  }
              }
          }
      }

      async fn on_typing(&self, sender: ConnId, user: UserId, on: bool) {
          let frame = Outbound::TypingUpdate {
              room_id: self.room_id.0.clone(),
              user_id: user.0,
              typing: on,
          };
          for (id, m) in &self.members {
              if *id != sender {
                  m.conn.send(frame.clone()).await;
              }
          }
      }

      fn presence(&self) -> Vec<String> {
          let mut users: Vec<String> = self.members.values().map(|m| m.user.0.clone()).collect();
          users.sort();
          users.dedup();
          users
      }

      async fn broadcast_presence(&self) {
          let frame = Outbound::PresenceUpdate {
              room_id: self.room_id.0.clone(),
              users: self.presence(),
          };
          for m in self.members.values() {
              m.conn.send(frame.clone()).await;
          }
      }
  }

  #[derive(Clone)]
  pub struct RoomHandle {
      tx: mpsc::Sender<RoomMsg>,
  }

  impl RoomHandle {
      pub fn spawn(room_id: RoomId) -> Self {
          let (tx, rx) = mpsc::channel(64);
          tokio::spawn(RoomActor { room_id, rx, members: HashMap::new() }.run());
          Self { tx }
      }

      /// True once the actor has stopped (its receiver dropped) — i.e. evicted.
      pub fn is_closed(&self) -> bool {
          self.tx.is_closed()
      }

      pub async fn join(&self, conn: ConnHandle, user: UserId) {
          let _ = self.tx.send(RoomMsg::Join { conn, user }).await;
      }
      pub async fn leave(&self, conn: ConnId) {
          let _ = self.tx.send(RoomMsg::Leave { conn }).await;
      }
      pub async fn typing(&self, conn: ConnId, user: UserId, on: bool) {
          let _ = self.tx.send(RoomMsg::Typing { conn, user, on }).await;
      }
  }
