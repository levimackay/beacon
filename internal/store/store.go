// Package store persists Beacon's targets, samples, incidents and audit
// trail in a local SQLite database, and applies the retention/rollup policy
// that keeps the file bounded in size.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store is Beacon's persistence layer: targets, samples, incidents and the
// audit log, plus the retention rollup.
type Store interface {
	UpsertTarget(ctx context.Context, t protocol.Target) error
	DeleteTarget(ctx context.Context, id string) error
	Targets(ctx context.Context) ([]protocol.Target, error)

	InsertSample(ctx context.Context, s protocol.Sample) error
	LatestSamples(ctx context.Context) (map[string]protocol.Sample, error)
	SampleSeries(ctx context.Context, targetID, metric string, since time.Time) ([]Point, error)

	OpenIncident(ctx context.Context, in protocol.Incident) (int64, error)
	ResolveIncident(ctx context.Context, targetID string, at time.Time) error
	OpenIncidents(ctx context.Context) ([]protocol.Incident, error)
	Incidents(ctx context.Context, f IncidentFilter) ([]protocol.Incident, error)

	Audit(ctx context.Context, principal, action, target, result string) error
	AuditTail(ctx context.Context, limit int) ([]AuditRow, error)
	Rollup(ctx context.Context, now time.Time) (RollupStats, error)
	Stats(ctx context.Context) (Stats, error)
	Close() error
}

// Point is one value in a metric's time series.
type Point struct {
	At    time.Time
	Value float64
}

// IncidentFilter narrows an Incidents query. A zero TargetID matches every
// target; a zero Since/Until leaves that bound open; a zero Limit means no
// limit.
type IncidentFilter struct {
	TargetID     string
	Since, Until time.Time
	Limit        int
}

// RollupStats summarises the work one Rollup call did.
type RollupStats struct {
	Rolled5m int64
	Rolled1h int64
	Pruned   int64
}

// AuditRow is one entry in the audit log.
type AuditRow struct {
	At        time.Time
	Principal string
	Action    string
	Target    string
	Result    string
}

// Stats is a point-in-time summary of what the store holds.
type Stats struct {
	Targets       int64
	RawSamples    int64
	Bucket5m      int64
	Bucket1h      int64
	OpenIncidents int64
	SizeBytes     int64
}

// bucket levels.
const (
	bucketRaw = 0
	bucket5m  = 300
	bucket1h  = 3600
)

// sentinelMetric is the metric name used to persist a sample that carries no
// metrics, so its state/latency/error still survive.
const sentinelMetric = ""

// Retention windows, per the plan's global constraints.
const (
	retentionRaw = 6 * time.Hour
	retention5m  = 7 * 24 * time.Hour
	retention1h  = 90 * 24 * time.Hour
)

type sqlStore struct {
	db    *sql.DB
	clock clock.Clock
}

// Open opens (creating if necessary) the SQLite database at path, applies
// the schema idempotently, and returns a Store backed by it. WAL mode,
// foreign keys and a 5s busy timeout are set via the connection DSN so they
// apply to every connection modernc.org/sqlite opens against the file.
func Open(path string, c clock.Clock) (Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite has a single writer; one connection avoids SQLITE_BUSY races
	// between goroutines instead of masking them with the busy timeout.
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}

	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}

	return &sqlStore{db: db, clock: c}, nil
}

func (s *sqlStore) Close() error { return s.db.Close() }

// rankState is the inverse of the windowRankCase SQL expression in
// retention.go: it maps the integer severity rank SQL computed for a
// window's worst state back to a protocol.State. Kept in lockstep with
// protocol.State's unexported rank ordering (down > unknown > warning >
// healthy).
func rankState(rank int64) protocol.State {
	switch rank {
	case 3:
		return protocol.StateDown
	case 2:
		return protocol.StateUnknown
	case 1:
		return protocol.StateWarning
	default:
		return protocol.StateHealthy
	}
}

// migrations are applied after the base schema, in order, to bring a
// database created by an older build up to date. CREATE TABLE IF NOT EXISTS
// does nothing to a table that already exists, so a column added later
// needs an explicit ALTER or it silently will not be there: values written
// to it are dropped and read back as their zero value, which for a security
// flag means a permission the user granted quietly stops applying.
//
// Each statement is expected to fail with "duplicate column name" once it
// has already been applied, which is how this stays idempotent without a
// version table. Anything else is a real error.
var migrations = []string{
	`ALTER TABLE targets ADD COLUMN allow_private INTEGER NOT NULL DEFAULT 0`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	for _, stmt := range migrations {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}
