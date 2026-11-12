package layoutedit

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
)

const (
	tokenParameter = "token"

	refused          = "lanhop answers only at the address it printed, on this machine"
	notAnArrangement = "The request is not an arrangement of the desk."
	fileHasProblems  = "The layout file has problems, so it cannot be shown here:\n%s\nFix those lines by hand, or remove the file, and load this page again."
)

//go:embed page.html
var page []byte

type arrangement struct {
	Rows [][]string `json:"rows"`
}

type view struct {
	Rows       [][]string `json:"rows"`
	Neighbours []string   `json:"neighbours"`
	Problem    string     `json:"problem"`
}

type desk struct {
	Own      string   `json:"own"`
	Machines []string `json:"machines"`
	Comments bool     `json:"comments"`
	view
}

type trouble struct {
	Problem string `json:"problem"`
}

func (e Editor) Handler(host, token string, done func()) http.Handler {
	routes := http.NewServeMux()
	routes.HandleFunc("GET /{$}", showPage)
	routes.HandleFunc("GET /desk", e.showDesk)
	routes.HandleFunc("POST /arrange", e.showArrangement)
	routes.HandleFunc("POST /save", e.saveArrangement)
	routes.HandleFunc("POST /done", func(response http.ResponseWriter, _ *http.Request) {
		answer(response, http.StatusOK, struct{}{})
		done()
	})
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sent := request.URL.Query().Get(tokenParameter)
		if request.Host != host || subtle.ConstantTimeCompare([]byte(sent), []byte(token)) != 1 {
			http.Error(response, refused, http.StatusForbidden)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		routes.ServeHTTP(response, request)
	})
}

func answer(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	json.NewEncoder(response).Encode(body)
}

func showPage(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Write(page)
}

func (e Editor) showDesk(response http.ResponseWriter, _ *http.Request) {
	written, err := e.Read()
	var problems layout.Problems
	switch {
	case errors.As(err, &problems):
		answer(response, http.StatusConflict, trouble{Problem: fmt.Sprintf(fileHasProblems, problems)})
	case err != nil:
		answer(response, http.StatusInternalServerError, trouble{Problem: err.Error()})
	default:
		answer(response, http.StatusOK, desk{
			Own:      e.Own,
			Machines: e.machines(),
			Comments: written.HasComments(),
			view:     e.look(written.Rows()),
		})
	}
}

func arrangementIn(response http.ResponseWriter, request *http.Request) (rows [][]string, understood bool) {
	var sent arrangement
	if err := json.NewDecoder(request.Body).Decode(&sent); err != nil {
		answer(response, http.StatusBadRequest, trouble{Problem: notAnArrangement})
		return nil, false
	}
	return sent.Rows, true
}

func (e Editor) showArrangement(response http.ResponseWriter, request *http.Request) {
	if rows, understood := arrangementIn(response, request); understood {
		answer(response, http.StatusOK, e.look(rows))
	}
}

func (e Editor) saveArrangement(response http.ResponseWriter, request *http.Request) {
	rows, understood := arrangementIn(response, request)
	if !understood {
		return
	}
	problem, err := e.save(rows)
	switch {
	case err != nil:
		answer(response, http.StatusInternalServerError, trouble{Problem: err.Error()})
	case problem != "":
		answer(response, http.StatusUnprocessableEntity, trouble{Problem: problem})
	default:
		answer(response, http.StatusOK, struct{}{})
	}
}
