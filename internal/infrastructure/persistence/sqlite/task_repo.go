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
	where := []string{"parent_task_id = ''", "type not in ('collect_machine_info', 'collect_static_info')"}
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
		select id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
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
			parent_task_id text not null default '',
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
	if err != nil {
		return err
	}
	if _, alterErr := r.db.Exec(`alter table tasks add column parent_task_id text not null default ''`); alterErr != nil && !strings.Contains(strings.ToLower(alterErr.Error()), "duplicate column") && !strings.Contains(strings.ToLower(alterErr.Error()), "already exists") {
		return alterErr
	}
	_, err = r.db.Exec(`create index if not exists idx_tasks_parent_created on tasks(parent_task_id, created_at desc)`)
	if err == nil {
		err = r.backfillMySQLInstallTaskParents()
	}
	if err == nil {
		err = r.backfillPlatformTaskParents()
	}
	return err
}

func (r *TaskRepository) backfillMySQLInstallTaskParents() error {
	rows, err := r.db.Query(`select parent.id, event.content from tasks as parent join task_events as event on event.task_id = parent.id where parent.type = 'mysql_cluster_bootstrap'`)
	if err != nil {
		return err
	}
	type parentEvent struct{ parentID, content string }
	events := make([]parentEvent, 0)
	for rows.Next() {
		var item parentEvent
		if err := rows.Scan(&item.parentID, &item.content); err != nil {
			_ = rows.Close()
			return err
		}
		events = append(events, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	children, err := r.db.Query(`select id from tasks where type = 'mysql_install' and parent_task_id = ''`)
	if err != nil {
		return err
	}
	relations := make(map[string]string)
	for children.Next() {
		var childID string
		if err := children.Scan(&childID); err != nil {
			_ = children.Close()
			return err
		}
		for _, item := range events {
			if strings.Contains(item.content, childID) {
				relations[childID] = item.parentID
				break
			}
		}
	}
	if err := children.Close(); err != nil {
		return err
	}
	for childID, parentID := range relations {
		if _, err := r.db.Exec(`update tasks set parent_task_id = ? where id = ? and parent_task_id = ''`, parentID, childID); err != nil {
			return err
		}
	}
	return nil
}

func (r *TaskRepository) backfillPlatformTaskParents() error {
	rows, err := r.db.Query(`select id, spec_json from tasks where type = 'platform_operation'`)
	if err != nil {
		return err
	}
	type relation struct {
		parent string
		child  string
	}
	var relations []relation
	for rows.Next() {
		var parentID, raw string
		if err := rows.Scan(&parentID, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		var spec taskdomain.PlatformOperationSpec
		if json.Unmarshal([]byte(raw), &spec) != nil {
			continue
		}
		for _, childID := range spec.RelatedTaskIDs {
			if childID = strings.TrimSpace(childID); childID != "" && childID != parentID {
				relations = append(relations, relation{parent: parentID, child: childID})
			}
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range relations {
		if _, err := r.db.Exec(`update tasks set parent_task_id = ? where id = ? and parent_task_id = ''`, item.parent, item.child); err != nil {
			return err
		}
	}
	return nil
}

func (r *TaskRepository) CreateTask(ctx context.Context, task taskdomain.Task, steps []taskdomain.Step, events []taskdomain.Event) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		insert into tasks (id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.ParentTaskID, string(task.Type), task.MachineID, task.AgentID, string(task.Status), task.ProgressPercent, task.CurrentStep, string(task.SpecJSON), formatDatabaseTime(task.CreatedAt), formatNullableTime(task.StartedAt), formatNullableTime(task.FinishedAt)); err != nil {
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
		select id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
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
		select id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks where parent_task_id = '' and type not in ('collect_machine_info', 'collect_static_info')
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
		select id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
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

func (r *TaskRepository) ListChildTasks(ctx context.Context, parentTaskID string) ([]taskdomain.Task, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, parent_task_id, type, machine_id, agent_id, status, progress_percent, current_step, spec_json, created_at, started_at, finished_at
		from tasks where parent_task_id = ? order by created_at asc, id asc
	`, parentTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []taskdomain.Task
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// AssignParentTasks attaches existing execution tasks to one business-level
// parent. Existing relationships are never overwritten.
func (r *TaskRepository) AssignParentTasks(ctx context.Context, parentTaskID string, childTaskIDs []string) error {
	parentTaskID = strings.TrimSpace(parentTaskID)
	if parentTaskID == "" {
		return errors.New("parent task id is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, childID := range childTaskIDs {
		childID = strings.TrimSpace(childID)
		if childID == "" || childID == parentTaskID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `update tasks set parent_task_id = ? where id = ? and parent_task_id = ''`, parentTaskID, childID); err != nil {
			return err
		}
	}
	return tx.Commit()
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

// DeleteTaskTree atomically removes a parent and every nested execution task.
func (r *TaskRepository) DeleteTaskTree(ctx context.Context, taskID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from tasks where id = ?`, taskID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("task not found")
	}
	ids := []string{taskID}
	frontier := []string{taskID}
	seen := map[string]bool{taskID: true}
	for len(frontier) > 0 {
		args := make([]any, len(frontier))
		for i := range frontier {
			args[i] = frontier[i]
		}
		rows, queryErr := tx.QueryContext(ctx, `select id from tasks where parent_task_id in (`+sqlPlaceholders(len(frontier))+`)`, args...)
		if queryErr != nil {
			return queryErr
		}
		next := make([]string, 0)
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
				next = append(next, id)
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return rowsErr
		}
		_ = rows.Close()
		frontier = next
	}
	args := make([]any, len(ids))
	for i := range ids {
		args[i] = ids[i]
	}
	placeholders := sqlPlaceholders(len(ids))
	for _, table := range []string{"task_events", "task_steps"} {
		query := fmt.Sprintf(`delete from %s where task_id in (%s)`, table, placeholders)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `delete from tasks where id in (`+placeholders+`)`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
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
	if err := scanner.Scan(&item.ID, &item.ParentTaskID, &taskType, &item.MachineID, &item.AgentID, &status, &item.ProgressPercent, &item.CurrentStep, &specJSON, &createdAt, &startedAt, &finishedAt); err != nil {
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
