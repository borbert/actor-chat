use serde::{Deserialize, Serialize};

/// Frames the CLIENT sends us. The JSON `type` field selects the variant;
/// per-variant camelCase matches the Go wire format (roomId, clientId, …).
#[derive(Debug, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Inbound {
    #[serde(rename_all = "camelCase")]
    Join { room_id: String },
    #[serde(rename_all = "camelCase")]
    Leave { room_id: String },
    #[serde(rename_all = "camelCase")]
    Send {
        room_id: String,
        body: String,
        client_id: String,
        #[serde(default)]
        token: Option<String>,
    },
    #[serde(rename_all = "camelCase")]
    TypingStart { room_id: String },
    #[serde(rename_all = "camelCase")]
    TypingStop { room_id: String },
    Ping {
        #[serde(default)]
        token: Option<String>,
    },
}

/// Frames WE send the client.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Outbound {
    #[serde(rename_all = "camelCase")]
    Hello {
        server: String,
        version: String,
    },
    Pong,
    #[serde(rename_all = "camelCase")]
    PresenceUpdate {
        room_id: String,
        users: Vec<String>,
    },

    #[serde(rename_all = "camelCase")]
    TypingUpdate {
        room_id: String,
        user_id: String,
        typing: bool,
    },
     #[serde(rename_all = "camelCase")]
      Ack {
          room_id: String,
          message_id: String,
          client_id: String,
          created_at: f64,
      },

    #[serde(rename_all = "camelCase")]
    Error {
        #[serde(skip_serializing_if = "Option::is_none")]
        room_id: Option<String>,
        #[serde(skip_serializing_if = "Option::is_none")]
        client_id: Option<String>,
        reason: String,
    },
    // Ack / PresenceUpdate / TypingUpdate land in M2–M4.
}
