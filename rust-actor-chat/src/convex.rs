use serde::de::DeserializeOwned;
  use serde::{Deserialize, Serialize};

  #[derive(Debug, thiserror::Error)]
  pub enum ConvexError {
      #[error("http error: {0}")]
      Http(#[from] reqwest::Error),
      #[error("convex {kind} {path} failed: {message}")]
      Function { kind: &'static str, path: String, message: String },
      #[error("decoding convex value: {0}")]
      Decode(#[from] serde_json::Error),
  }

  /// Minimal Convex HTTP client. Port of the Go `internal/convex` client.
  #[derive(Clone)]
  pub struct ConvexClient {
      base_url: String,
      http: reqwest::Client,
      token: Option<String>,
  }

  /// The fields we need back from a persisted message.
  #[derive(Debug, Deserialize)]
  pub struct SentMessage {
      #[serde(rename = "_id")]
      pub id: String,
      #[serde(rename = "createdAt")]
      pub created_at: f64,
  }

  #[derive(Serialize)]
  struct RequestBody<'a, A: Serialize> {
      path: &'a str,
      args: A,
      format: &'a str,
  }

  #[derive(Deserialize)]
  #[serde(rename_all = "camelCase")]
  struct ResponseBody {
      status: String,
      #[serde(default)]
      value: serde_json::Value,
      #[serde(default)]
      error_message: Option<String>,
  }

  /// A Convex user document — we only need the id.
  #[derive(Debug, Deserialize)]
  pub struct ConvexUser {
      #[serde(rename = "_id")]
      pub id: String,
  }

  impl ConvexClient {
      pub fn new(base_url: impl Into<String>) -> Self {
          Self {
              base_url: base_url.into().trim_end_matches('/').to_string(),
              http: reqwest::Client::new(),
              token: None,
          }
      }

      /// A clone that authenticates as the bearer of `token`, so the mutation runs
      /// with the user's Convex identity (mirrors Go's `WithAuth`).
      pub fn with_auth(&self, token: impl Into<String>) -> Self {
          Self { token: Some(token.into()), ..self.clone() }
      }

      pub async fn query<A: Serialize, T: DeserializeOwned>(
          &self,
          path: &str,
          args: A,
      ) -> Result<T, ConvexError> {
          self.call("query", path, args).await
      }

      pub async fn mutation<A: Serialize, T: DeserializeOwned>(
          &self,
          path: &str,
          args: A,
      ) -> Result<T, ConvexError> {
          self.call("mutation", path, args).await
      }

      async fn call<A: Serialize, T: DeserializeOwned>(
          &self,
          kind: &'static str,
          path: &str,
          args: A,
      ) -> Result<T, ConvexError> {
          let url = format!("{}/api/{}", self.base_url, kind);
          let mut req = self.http.post(url).json(&RequestBody { path, args, format: "json" });
          if let Some(token) = &self.token {
              req = req.bearer_auth(token);
          }
          let body: ResponseBody = req.send().await?.json().await?;
          if body.status != "success" {
              return Err(ConvexError::Function {
                  kind,
                  path: path.to_string(),
                  message: body.error_message.unwrap_or_else(|| "unknown error".into()),
              });
          }
          Ok(serde_json::from_value(body.value)?)
      }
  }
