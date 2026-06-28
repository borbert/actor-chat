 use crate::actors::connection::{self, Identity};
  use crate::actors::UserId;
  use crate::convex::ConvexUser;
  use crate::AppState;
  use axum::extract::ws::WebSocketUpgrade;
  use axum::extract::{Query, State};
  use axum::http::{header, StatusCode};
  use axum::response::{IntoResponse, Response};
  use axum::routing::get;
  use axum::{Json, Router};
  use serde::Deserialize;
  use serde_json::json;

  pub fn router(state: AppState) -> Router {
      Router::new()
          .route("/health", get(health))
          .route("/version", get(version))
          .route("/ws", get(ws_handler))
         .route("/ready", get(ready))
          .with_state(state)
  }

  async fn health() -> impl IntoResponse {
      ([(header::SERVER, server_header())], "ok")
  }

  async fn version() -> impl IntoResponse {
      let body = Json(json!({
          "backend": "rust-actor-chat",
          "runtime": "tokio",
          "version": env!("CARGO_PKG_VERSION"),
      }));
      ([(header::SERVER, server_header())], body)
  }

  fn server_header() -> String {
      format!("rust-actor-chat/{}", env!("CARGO_PKG_VERSION"))
  }

  #[derive(Deserialize)]
  struct ConnectParams {
      token: Option<String>,
  }

  async fn ws_handler(
      State(state): State<AppState>,
      Query(params): Query<ConnectParams>,
      ws: WebSocketUpgrade,
  ) -> Response {
      // 1. token required (browsers can't set WS headers, so it rides the query string)
      let Some(token) = params.token else {
          return (StatusCode::UNAUTHORIZED, "missing token").into_response();
      };

      // 2. validate the Clerk JWT -> subject (Clerk user id)
      let sub = match state.validator.validate(&token).await {
          Ok(sub) => sub,
          Err(e) => {
              tracing::warn!("token rejected: {e}");
              return (StatusCode::UNAUTHORIZED, "invalid token").into_response();
          }
      };

      // 3. resolve the Convex user id (mutation runs AS the user via bearer token)
      let user = match state
          .convex
          .with_auth(&token)
          .mutation::<_, ConvexUser>("users:getOrCreateFromAuth", json!({}))
          .await
      {
          Ok(u) => UserId(u.id),
          Err(e) => {
              tracing::error!("user provisioning failed: {e}");
              return (StatusCode::INTERNAL_SERVER_ERROR, "user provisioning failed").into_response();
          }
      };

      tracing::info!(sub = %sub, user = %user.0, "ws authenticated");

      let identity = Identity { user, auth_id: sub, token };
      ws.on_upgrade(move |socket| connection::run(socket, state, identity))
  }

 async fn ready(State(state): State<AppState>) -> Response {
      match state
          .convex
          .query::<_, serde_json::Value>("health:ping", json!({}))
          .await
      {
          Ok(_) => (StatusCode::OK, "ready").into_response(),
          Err(e) => {
              tracing::warn!("readiness check failed: {e}");
              (StatusCode::SERVICE_UNAVAILABLE, "not ready").into_response()
          }
      }
  }
