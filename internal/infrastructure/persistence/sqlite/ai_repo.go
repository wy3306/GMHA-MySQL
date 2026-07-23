package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	aidomain "gmha/internal/domain/ai"
)

// AIRepository stores the small AI control-plane state as one versioned JSON
// document. Model secrets are encrypted by the application service before this
// repository sees them.
type AIRepository struct {
	db *DB
	mu sync.Mutex
}

func NewAIRepository(db *DB) *AIRepository { return &AIRepository{db: db} }

func (r *AIRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists ai_control_state (
			id text primary key,
			payload text not null,
			updated_at text not null
		);
	`)
	return err
}

func (r *AIRepository) Load(ctx context.Context) (aidomain.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.load(ctx)
}

func (r *AIRepository) load(ctx context.Context) (aidomain.State, error) {
	var payload string
	err := r.db.QueryRowContext(ctx, `select payload from ai_control_state where id = ?`, "default").Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return aidomain.State{}, nil
	}
	if err != nil {
		return aidomain.State{}, err
	}
	var state aidomain.State
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return aidomain.State{}, err
	}
	return state, nil
}

func (r *AIRepository) Save(ctx context.Context, state aidomain.State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		insert into ai_control_state(id, payload, updated_at) values(?, ?, CURRENT_TIMESTAMP)
		on conflict(id) do update set payload = excluded.payload, updated_at = excluded.updated_at
	`, "default", string(payload))
	return err
}
