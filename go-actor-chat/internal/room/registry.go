package room

import (
	"sync"

	"github.com/anthdm/hollywood/actor"
)

const actorKind = "room"

// Registry maps roomId → room actor PID, spawning lazily on first use
// (PRD §9). It is the only public API for resolving a room's actor.
type Registry struct {
	mu     sync.Mutex
	engine *actor.Engine
	store  Store
}

func NewRegistry(engine *actor.Engine, store Store) *Registry {
	return &Registry{engine: engine, store: store}
}

// PIDFor returns the PID of the actor for roomID, spawning it if absent.
// Actors evicted after idle self-poison are respawned transparently.
func (r *Registry) PIDFor(roomID string) *actor.PID {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pid := r.engine.Registry.GetPID(actorKind, roomID); pid != nil {
		return pid
	}
	return r.engine.Spawn(NewActor(roomID, r.store), actorKind, actor.WithID(roomID))
}
