package ping

import (
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
)


func TestPingActor(t *testing.T) {
 eng, err := actor.NewEngine(actor.NewEngineConfig())
        if err != nil { t.Fatal(err) }

        pid := eng.Spawn(New, "ping") // or package ping_test / internal test

        resp := eng.Request(pid, Ping{Nonce: "abc"}, time.Second)
        res, err := resp.Result()
        if err != nil { t.Fatalf("request failed: %v", err) }

        pong, ok := res.(Pong)
        if !ok { t.Fatalf("expected Pong, got %T", res) }
        if pong.Nonce != "abc" { t.Errorf("nonce not echoed: got %q", pong.Nonce) }
}
