use std::collections::HashMap;
  use std::sync::{Arc, Mutex};

  use crate::actors::room::RoomHandle;
  use crate::actors::RoomId;

  #[derive(Clone, Default)]
  pub struct RoomRegistry {
      inner: Arc<Mutex<HashMap<RoomId, RoomHandle>>>,
  }

  impl RoomRegistry {
      pub fn new() -> Self {
          Self::default()
      }

      /// Handle for a room, spawning on first use — or respawning if the previous
      /// actor self-evicted after going idle.
      pub fn handle_for(&self, room_id: &RoomId) -> RoomHandle {
          let mut map = self.inner.lock().expect("registry mutex poisoned");
          if let Some(existing) = map.get(room_id)
              && !existing.is_closed()
          {
              return existing.clone();
          }
          let handle = RoomHandle::spawn(room_id.clone());
          map.insert(room_id.clone(), handle.clone());
          handle
      }
  }


