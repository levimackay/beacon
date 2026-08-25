package collect

import (
	"context"

	"github.com/levimackay/beacon/internal/protocol"
)

// Collector produces one Sample for a Target. It never returns a Go error:
// a failed collection is a normal outcome (the host is down, the sensor
// isn't wired up) and is encoded in the Sample's State and Error fields
// instead, so callers never need a second error-handling path alongside it.
type Collector interface {
	Collect(ctx context.Context, t protocol.Target) protocol.Sample
}
