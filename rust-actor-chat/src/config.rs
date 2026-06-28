use anyhow::Context;

  #[derive(Debug, Clone)]
  pub struct Settings {
      pub port: u16,
      pub convex_url: String,
      pub clerk_issuer: String,
  }

  impl Settings {
      pub fn load() -> anyhow::Result<Self> {
          let port = match std::env::var("PORT") {
              Ok(p) => p.parse().context("PORT must be a number")?,
              Err(_) => 8090,
          };
          let convex_url = std::env::var("CONVEX_URL").context("CONVEX_URL must be set")?;
          let clerk_issuer = std::env::var("CLERK_JWT_ISSUER_DOMAIN")
              .context("CLERK_JWT_ISSUER_DOMAIN must be set")?;
          Ok(Self { port, convex_url, clerk_issuer })
      }
  }
