pub mod actors;
  pub mod auth;
  pub mod config;
  pub mod convex;
  pub mod protocol;
  pub mod routes;
  pub mod telemetry;

  use std::sync::Arc;

  use anyhow::Context;
  use tokio::net::TcpListener;

  use crate::actors::registry::RoomRegistry;
  use crate::auth::TokenValidator;
  use crate::convex::ConvexClient;

  /// Shared state handed to every request via axum's `State` extractor.
  #[derive(Clone)]
  pub struct AppState {
      pub registry: RoomRegistry,
      pub validator: Arc<TokenValidator>, // not Clone (holds a lock) -> share via Arc
      pub convex: ConvexClient,
  }

  pub async fn run() -> anyhow::Result<()> {
      let settings = config::Settings::load()?;
      let addr = format!("0.0.0.0:{}", settings.port);

      // Fetches the Clerk JWKS once at startup (fails fast if the issuer is wrong).
      let validator = Arc::new(
          TokenValidator::new(settings.clerk_issuer)
              .await
              .context("building Clerk token validator")?,
      );
      let convex = ConvexClient::new(settings.convex_url);
      let registry = RoomRegistry::new();
      let state = AppState { registry, validator, convex };

      let listener = TcpListener::bind(&addr)
          .await
          .with_context(|| format!("binding {addr}"))?;
      tracing::info!("listening on http://{addr}");

      axum::serve(listener, routes::router(state))
          .with_graceful_shutdown(shutdown_signal())
          .await
          .context("server error")?;
      Ok(())
  }

async fn shutdown_signal() {
      use tokio::signal;

      let ctrl_c = async {
          signal::ctrl_c().await.expect("install Ctrl-C handler");
      };

      #[cfg(unix)]
      let terminate = async {
          signal::unix::signal(signal::unix::SignalKind::terminate())
              .expect("install SIGTERM handler")
              .recv()
              .await;
      };
      #[cfg(not(unix))]
      let terminate = std::future::pending::<()>();

      tokio::select! {
          _ = ctrl_c => {},
          _ = terminate => {},
      }
      tracing::info!("shutdown signal received, draining");
  }
