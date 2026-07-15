package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	taskdomain "gmha/internal/domain/task"
)

// TaskRepository 是任务实体的 SQLite 仓储实现。
type TaskRepository struct {
	db *DB
}

func (r *TaskRepository) ListTaskPage(ctx context.Context, query taskdomain.ListQuery) ([]taskdomain.Task, int, error) {
	where := make([]string, 0, 3)
	args := make([]any, 0)
	if keyword := strings.ToLower(strings.TrimSpace(query.Keyword)); keyword != "" {
		where = append(where, `(lower(id) like ? or lower(type) like ? or lower(machine_id) like ? or lower(current_step) like ? or lower(spec_json) like ?)`)
		like := "%" + keyword + "%"
		args = append(args, like, like, like, like, like)
	}
	if len(query.Statuses) > 0 {
		placeholders := make([]string, len(query.Statuses))
		for i, status := range query.Statuses {
			placeholders[i] = "?"
			args = append(args, string(status))
		}
		where = append(where, "status in ("+strings.Join(placeholders, ",")+")")
	}
	if len(query.Types) > 0 {
		placeholders := make([]string, len(query.Types))
		for i, taskType := range query.Types {
			placeholders[i] = "?"
			args = append(args, string(taskType))
		}
		where = append(where, "type in ("+strings.Join(placeholders, ",")+")")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "select count(*) from tasks"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageArgs := append(append([]any{}, args...), query.Limit, query.Offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		select id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks%s order by created_at desc, id desc limit ? offset ?`, whereSQL), pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]taskdomain.Task, 0, query.Limit)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func NewTaskRepository(db *DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists tasks (
			id text primary key,
			type text not null,
			machine_id text not null,
			agent_id text not null,
			status text not null,
			progress_percent integer not null default 0,
			current_step text not null default '',
			spec_json text not null default '',
			created_at text not null,
			started_at text,
			finished_at text
		);
		create index if not exists idx_tasks_machine_created on tasks(machine_id, created_at desc);
		create table if not exists task_steps (
			id text primary key,
			task_id text not null,
			step_no integer not null,
			step_name text not null,
			status text not null,
			message text not null default '',
			started_at text,
			finished_at text
		);
		create index if not exists idx_task_steps_task_no on task_steps(task_id, step_no);
		create table if not exists task_events (
			id text primary key,
			task_id text not null,
			step_id text not null default '',
			event_type text not null,
			content text not null,
			created_at text not null
		);
		create index if not exists idx_task_events_task_time on task_events(task_id, created_at desc);
	`)
	return err
}

func (r *TaskRepository) CreateTask(ctx context.Context, task taskdomain.Task, steps []taskdomain.Step, events []taskdomain.Event) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		insert into tasks (id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, string(task.Type), task.MachineID, task.AgentID, string(task.Status), task.ProgressPercent, task.CurrentStep, string(task.SpecJSON), formatDatabaseTime(task.CreatedAt), formatNullableTime(task.StartedAt), formatNullableTime(task.FinishedAt)); err != nil {
		return err
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, `
			insert into task_steps (id, task_id, step_no, step_name, status, message, started_at, finished_at)
			values (?, ?, ?, ?, ?, ?, ?, ?)
		`, step.ID, step.TaskID, step.StepNo, step.StepName, string(step.Status), step.Message, formatNullableTime(step.StartedAt), formatNullableTime(step.FinishedAt)); err != nil {
			return err
		}
	}
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `
			insert into task_events (id, task_id, step_id, event_type, content, created_at)
			values (?, ?, ?, ?, ?, ?)
		`, event.ID, event.TaskID, event.StepID, string(event.EventType), event.Content, formatDatabaseTime(event.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *TaskRepository) GetTask(ctx context.Context, taskID string) (taskdomain.Task, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks where id = ?
	`, taskID)
	item, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskdomain.Task{}, false, nil
		}
		return taskdomain.Task{}, false, err
	}
	return item, true, nil
}

func (r *TaskRepository) ListTasks(ctx context.Context, limit int) ([]taskdomain.Task, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		select id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks
		order by created_at desc
		limit ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]taskdomain.Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *TaskRepository) ListTasksByStatus(ctx context.Context, status taskdomain.Status, limit int) ([]taskdomain.Task, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		select id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks
		where status = ?
		order by created_at asc
		limit ?
	`, string(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]taskdomain.Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *TaskRepository) ListSteps(ctx context.Context, taskID string) ([]taskdomain.Step, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, task_id, step_no, step_name, status, message, started_at, finished_at
		from task_steps where task_id = ? order by step_no asc
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]taskdomain.Step, 0)
	for rows.Next() {
		item, err := scanTaskStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *TaskRepository) ListEvents(ctx context.Context, taskID string, limit int) ([]taskdomain.Event, error) {
	if limit == 0 {
		limit = 50
	}
	query := `
		select id, task_id, step_id, event_type, content, created_at
		from task_events where task_id = ? order by created_at asc, id asc`
	args := []any{taskID}
	if limit > 0 {
		query += " limit ?"
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]taskdomain.Event, 0)
	for rows.Next() {
		item, err := scanTaskEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *TaskRepository) UpdateTask(ctx context.Context, task taskdomain.Task) error {
	_, err := r.db.ExecContext(ctx, `
		update tasks
		set status = ?, progress_percent = ?, current_step = ?, started_at = ?, finished_at = ?, spec_json = ?
		where id = ?
	`, string(task.Status), task.ProgressPercent, task.CurrentStep, formatNullableTime(task.StartedAt), formatNullableTime(task.FinishedAt), string(task.SpecJSON), task.ID)
	return err
}

func (r *TaskRepository) UpdateStep(ctx context.Context, step taskdomain.Step) error {
	_, err := r.db.ExecContext(ctx, `
		update task_steps
		set status = ?, message = ?, started_at = ?, finished_at = ?
		where id = ?
	`, string(step.Status), step.Message, formatNullableTime(step.StartedAt), formatNullableTime(step.FinishedAt), step.ID)
	return err
}

func (r *TaskRepository) AppendEvent(ctx context.Context, event taskdomain.Event) error {
	_, err := r.db.ExecContext(ctx, `
		insert into task_events (id, task_id, step_id, event_type, content, created_at)
		values (?, ?, ?, ?, ?, ?)
	`, event.ID, event.TaskID, event.StepID, string(event.EventType), event.Content, formatDatabaseTime(event.CreatedAt))
	return err
}

// DeleteTask atomically removes a task and its complete step/event history.
func (r *TaskRepository) DeleteTask(ctx context.Context, taskID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from task_events where task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from task_steps where task_id = ?`, taskID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `delete from tasks where id = ?`, taskID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("task not found")
	}
	return tx.Commit()
}

func (r *TaskRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from task_events where task_id in (select id from tasks where machine_id = ?)`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from task_steps where task_id in (select id from tasks where machine_id = ?)`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from tasks where machine_id = ?`, machineID); err != nil {
		return err
	}
	return tx.Commit()
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner taskScanner) (taskdomain.Task, error) {
	var item taskdomain.Task
	var taskType, status, createdAt string
	var specJSON sql.NullString
	var startedAt, finishedAt sql.NullString
	if err := scanner.Scan(&item.ID, &taskType, &item.MachineID, &item.AgentID, &status, &item.ProgressPercent, &item.CurrentStep, &specJSON, &createdAt, &startedAt, &finishedAt); err != nil {
		return taskdomain.Task{}, err
	}
	item.Type = taskdomain.Type(taskType)
	item.Status = taskdomain.Status(status)
	if specJSON.Valid && specJSON.String != "" {
		item.SpecJSON = json.RawMessage(specJSON.String)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.StartedAt = parseNullableTime(startedAt)
	item.FinishedAt = parseNullableTime(finishedAt)
	return item, nil
}

func scanTaskStep(scanner taskScanner) (taskdomain.Step, error) {
	var item taskdomain.Step
	var status string
	var startedAt, finishedAt sql.NullString
	if err := scanner.Scan(&item.ID, &item.TaskID, &item.StepNo, &item.StepName, &status, &item.Message, &startedAt, &finishedAt); err != nil {
		return taskdomain.Step{}, err
	}
	item.Status = taskdomain.StepStatus(status)
	item.StartedAt = parseNullableTime(startedAt)
	item.FinishedAt = parseNullableTime(finishedAt)
	return item, nil
}

func scanTaskEvent(scanner taskScanner) (taskdomain.Event, error) {
	var item taskdomain.Event
	var eventType, createdAt string
	if err := scanner.Scan(&item.ID, &item.TaskID, &item.StepID, &eventType, &item.Content, &createdAt); err != nil {
		return taskdomain.Event{}, err
	}
	item.EventType = taskdomain.EventType(eventType)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return item, nil
}
