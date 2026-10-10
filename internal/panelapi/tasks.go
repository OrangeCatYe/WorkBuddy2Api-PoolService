package panelapi

import (
	"net/http"
	"strconv"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/taskrun"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// SetTaskRuns 注入任务执行记录。
func (a *API) SetTaskRuns(r *taskrun.Recorder) { a.runs = r }

func (a *API) registerTaskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/task-runs", a.auth(a.listTaskRuns))
	mux.HandleFunc("GET /api/task-runs/latest", a.auth(a.latestTaskRuns))
	mux.HandleFunc("GET /api/task-runs/{id}", a.auth(a.getTaskRun))
	mux.HandleFunc("GET /api/providers/{pid}/accounts/{aid}/quests", a.auth(a.listQuests))
	mux.HandleFunc("POST /api/providers/{pid}/accounts/{aid}/quests/run", a.auth(a.runQuests))
	mux.HandleFunc("POST /api/providers/{pid}/accounts/{aid}/quests/accept", a.auth(a.acceptQuests))
	mux.HandleFunc("POST /api/providers/{pid}/accounts/{aid}/quests/{code}/claim", a.auth(a.claimQuest))
}

// listTaskRuns ?provider=&task=&status=&limit=
func (a *API) listTaskRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	runs := a.runs.List(taskrun.Filter{Provider: q.Get("provider"), Task: q.Get("task"), Status: q.Get("status"), Limit: limit})
	if runs == nil {
		runs = []taskrun.Run{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (a *API) latestTaskRuns(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"latest": a.runs.Latest()})
}

func (a *API) getTaskRun(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runs.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "执行记录不存在或已过期")
		return
	}
	if run.Items == nil {
		run.Items = []taskrun.Item{}
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (a *API) quests(w http.ResponseWriter, r *http.Request) (provider.QuestCapable, bool) {
	p, ok := a.provider(w, r)
	if !ok {
		return nil, false
	}
	q, ok := p.(provider.QuestCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台没有账号任务")
	}
	return q, ok
}

func (a *API) listQuests(w http.ResponseWriter, r *http.Request) {
	q, ok := a.quests(w, r)
	if !ok {
		return
	}
	list, err := q.Quests(r.Context(), r.PathValue("aid"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if list == nil {
		list = []provider.Quest{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"quests": list})
}

// runQuests 请求体 {"codes": [...]}，省略 = 全部待办。异步执行，返回执行记录 ID。
func (a *API) runQuests(w http.ResponseWriter, r *http.Request) {
	q, ok := a.quests(w, r)
	if !ok {
		return
	}
	var body struct {
		Codes []string `json:"codes"`
	}
	_ = decodeBody(r, &body)
	id, err := q.StartQuests(r.PathValue("aid"), body.Codes)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "run_id": id})
}

func (a *API) acceptQuests(w http.ResponseWriter, r *http.Request) {
	q, ok := a.quests(w, r)
	if !ok {
		return
	}
	n, err := q.AcceptQuests(r.Context(), r.PathValue("aid"))
	if err != nil && n == 0 {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]any{"ok": true, "accepted": n}
	if err != nil {
		out["message"] = err.Error()
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) claimQuest(w http.ResponseWriter, r *http.Request) {
	q, ok := a.quests(w, r)
	if !ok {
		return
	}
	res, err := q.ClaimQuest(r.Context(), r.PathValue("aid"), r.PathValue("code"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
