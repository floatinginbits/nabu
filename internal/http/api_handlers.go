package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/task"
)

// apiServer implements the generated api.ServerInterface: parse the request,
// call one service method, translate the result or error. No business logic.
type apiServer struct {
	log   *slog.Logger
	tasks *task.Service
}

func (s *apiServer) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, api.Health{Status: "ok"})
}

func (s *apiServer) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req api.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON body")
		return
	}
	t, err := s.tasks.Create(r.Context(), req.Title)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPITask(t))
}

func (s *apiServer) ListTasks(w http.ResponseWriter, r *http.Request, params api.ListTasksParams) {
	var p task.ListParams
	if params.Status != nil {
		st := task.Status(*params.Status)
		p.Status = &st
	}
	if params.Cursor != nil {
		p.Cursor = *params.Cursor
	}
	if params.PageSize != nil {
		p.PageSize = *params.PageSize
	}

	res, err := s.tasks.List(r.Context(), p)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	out := api.TaskList{Data: make([]api.Task, len(res.Tasks))}
	for i, t := range res.Tasks {
		out.Data[i] = toAPITask(t)
	}
	if res.NextCursor != "" {
		out.NextCursor = &res.NextCursor
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *task.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", ve.Msg)
		return
	}
	s.log.ErrorContext(r.Context(), "handler error",
		slog.String("request_id", RequestIDFromContext(r.Context())),
		slog.Any("error", err),
	)
	writeError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
}

func toAPITask(t task.Task) api.Task {
	return api.Task{
		Id:        t.ID,
		Title:     t.Title,
		Status:    api.TaskStatus(t.Status),
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}
