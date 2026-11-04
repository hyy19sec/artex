package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/db"
)

// 测评模板（全局参考数据）+ 任务应用模板/读取定级口径。
// 模板是快照来源：任务创建或手动应用时按条款 scope 复制到
// task_constraints(scope='rating'|'operation')；report_finding 的 rating_clause_id
// 引用的是 rating 快照行 id，不是模板条款 id。表名沿用 rating_templates 以兼容旧库。

func (s *Server) listRatingTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.m.PG().ListRatingTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"templates": rows})
}

func (s *Server) createRatingTemplate(w http.ResponseWriter, r *http.Request) {
	var in db.RatingTemplateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	t, err := s.m.PG().CreateRatingTemplate(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) updateRatingTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad rating template id")
		return
	}
	var in db.RatingTemplateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	t, err := s.m.PG().UpdateRatingTemplate(id, in)
	if err != nil {
		if errors.Is(err, db.ErrRatingTemplateNotFound) {
			writeErr(w, 404, "测评模板不存在")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) deleteRatingTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad rating template id")
		return
	}
	ok, err := s.m.PG().DeleteRatingTemplate(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "评级模板不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// taskRatingCriteria returns the task's selected template + active rating clauses.
func (s *Server) taskRatingCriteria(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	criteria, err := t.Store.RatingCriteria()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, criteria)
}

// setTaskRatingTemplate snapshots a template into an existing task's constraints.
// mode=replace supersedes active rating rows and template-sourced operation rows;
// mode=append only adds clauses not already active.
func (s *Server) setTaskRatingTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "任务正在删除,无法修改测评模板")
		return
	}
	defer s.engine.decInflight(t.ID)

	var body struct {
		TemplateID int64  `json:"template_id"`
		Mode       string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if body.TemplateID <= 0 {
		writeErr(w, 400, "template_id 无效")
		return
	}
	replace := true
	switch body.Mode {
	case "", "replace":
		replace = true
	case "append":
		replace = false
	default:
		writeErr(w, 400, "mode 必须是 replace 或 append")
		return
	}
	added, err := s.m.PG().ApplyRatingTemplate(t.ExpID, body.TemplateID, replace)
	if err != nil {
		if errors.Is(err, db.ErrRatingTemplateNotFound) {
			writeErr(w, 404, "测评模板不存在")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	if body.TemplateID > 0 {
		// Keep the in-memory task's display field in sync until the next reload.
		t.RatingTemplateID = &body.TemplateID
	}
	writeJSON(w, 200, map[string]any{"ok": true, "added": added})
}
