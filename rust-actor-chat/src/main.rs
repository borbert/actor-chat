use anyhow::Result;

#[tokio::main]
async fn main() -> Result<()> {
    dotenvy::from_filename(".env.local").ok();
    dotenvy::dotenv().ok();
    rust_actor_chat::telemetry::init();
    rust_actor_chat::run().await
}
