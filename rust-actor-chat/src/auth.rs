use std::collections::HashMap;
  use std::sync::RwLock;

  use jsonwebtoken::jwk::{AlgorithmParameters, JwkSet};
  use jsonwebtoken::{decode, decode_header, Algorithm, DecodingKey, Validation};
  use serde::Deserialize;

  #[derive(Debug, thiserror::Error)]
  pub enum AuthError {
      #[error("fetching JWKS: {0}")]
      Jwks(#[from] reqwest::Error),
      #[error("invalid token: {0}")]
      Token(#[from] jsonwebtoken::errors::Error),
      #[error("token missing `kid` header")]
      MissingKid,
      #[error("no signing key matches kid {0}")]
      UnknownKid(String),
  }

  /// We only care about the subject (the Clerk user id). jsonwebtoken validates
  /// iss / aud / exp against the raw token separately — they need not be here.
  #[derive(Deserialize)]
  struct Claims {
      sub: String,
  }

  /// Validates Clerk-issued JWTs against the instance JWKS (RS256, aud = "convex").
  /// Port of the Go `TokenValidator`.
  pub struct TokenValidator {
      issuer: String,
      jwks_url: String,
      http: reqwest::Client,
      keys: RwLock<HashMap<String, DecodingKey>>, // kid -> key
  }

  impl TokenValidator {
      pub async fn new(issuer: impl Into<String>) -> Result<Self, AuthError> {
          let issuer = issuer.into();
          let jwks_url = format!("{}/.well-known/jwks.json", issuer.trim_end_matches('/'));
          let http = reqwest::Client::new();
          let keys = fetch_keys(&http, &jwks_url).await?;
          Ok(Self { issuer, jwks_url, http, keys: RwLock::new(keys) })
      }

      /// Validate a token; returns the Clerk subject (user id) on success.
      pub async fn validate(&self, token: &str) -> Result<String, AuthError> {
          let kid = decode_header(token)?.kid.ok_or(AuthError::MissingKid)?;

          // Cache hit, or refetch once in case Clerk rotated its signing keys.
          let key = match self.cached_key(&kid) {
              Some(k) => k,
              None => self.refresh_and_get(&kid).await?,
          };

          let mut validation = Validation::new(Algorithm::RS256);
          validation.set_issuer(&[&self.issuer]);
          validation.set_audience(&["convex"]); // set by Clerk's "convex" JWT template
          // exp is validated by default

          Ok(decode::<Claims>(token, &key, &validation)?.claims.sub)
      }

      fn cached_key(&self, kid: &str) -> Option<DecodingKey> {
          self.keys.read().expect("jwks lock poisoned").get(kid).cloned()
      }

      async fn refresh_and_get(&self, kid: &str) -> Result<DecodingKey, AuthError> {
          let fresh = fetch_keys(&self.http, &self.jwks_url).await?;
          let found = fresh.get(kid).cloned();
          *self.keys.write().expect("jwks lock poisoned") = fresh;
          found.ok_or_else(|| AuthError::UnknownKid(kid.to_string()))
      }
  }

  async fn fetch_keys(
      http: &reqwest::Client,
      jwks_url: &str,
  ) -> Result<HashMap<String, DecodingKey>, AuthError> {
      let set: JwkSet = http.get(jwks_url).send().await?.error_for_status()?.json().await?;
      let mut keys = HashMap::new();
      for jwk in &set.keys {
          if let (Some(kid), AlgorithmParameters::RSA(_)) = (jwk.common.key_id.clone(), &jwk.algorithm)
              && let Ok(key) = DecodingKey::from_jwk(jwk)
          {
              keys.insert(kid, key);
          }
      }
      Ok(keys)
  }
