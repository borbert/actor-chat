pub mod connection;
pub mod registry;
pub mod room;

use std::sync::atomic::{AtomicU64, Ordering};
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct ConnId(u64);

impl ConnId {
    pub fn next() -> Self {
        static COUNTER: AtomicU64 = AtomicU64::new(1);
        ConnId(COUNTER.fetch_add(1, Ordering::Relaxed))
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct RoomId(pub String);
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct UserId(pub String);
