package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// adoServer emulates the part of the Azure DevOps REST API that taggr uses,
// including the endpoint discovery the SDK performs before its first call. The
// real SDK talks to it, so the integration tests cover URL building,
// authentication, JSON encoding and pagination rather than mocking them away.
type adoServer struct {
	*httptest.Server

	// Project and Repository the server answers for.
	Project    string
	Repository string
	// Token the client has to authenticate with.
	Token string

	mu sync.Mutex
	// Tags maps a tag name to the commit it points at.
	Tags map[string]string
	// AnnotatedTags holds the tags whose ref points at a tag object; their
	// PeeledCommit is what the tag really refers to.
	AnnotatedTags map[string]string
	// Branches maps a branch name to the commit at its tip.
	Branches map[string]string
	// DefaultBranch is the ref returned as the repository's default branch.
	DefaultBranch string
	// Commits is the history returned for a commit range, newest first.
	Commits []ServerCommit
	// Labels maps a pull request ID to its labels.
	Labels map[int][]ServerLabel
	// PullRequestForCommit maps a merge commit to the pull request that produced it.
	PullRequestForCommit map[string]int
	// TagPageSize splits the tag listing into pages when set, which exercises the
	// continuation token handling.
	TagPageSize int
	// RejectTagCreation makes a lightweight tag creation fail the way Azure DevOps
	// reports a rejected ref update: inside the result, not as an HTTP error.
	RejectTagCreation string

	// CreatedAnnotated records the annotated tags that were created.
	CreatedAnnotated []CreatedTag
	// CreatedLightweight records the ref updates that created lightweight tags.
	CreatedLightweight []CreatedTag
	// Requests records every request that arrived, as "METHOD /path".
	Requests []string
}

// clientToken is the token taggr authenticates with in the tests. A server whose
// Token differs from it rejects every request, which is how the tests provoke an
// authentication failure.
const clientToken = "test-token"

// ServerCommit is a commit the server hands out.
type ServerCommit struct {
	ID string
	// Message is the full commit message.
	Message string
	// TruncatedIn shortens the message in the commit listing, the way Azure DevOps
	// truncates long messages, so that only fetching the single commit reveals it.
	TruncatedIn string
}

// ServerLabel is a label on a pull request.
type ServerLabel struct {
	Name   string
	Active bool
}

// CreatedTag is a tag creation the server accepted.
type CreatedTag struct {
	Name    string
	Commit  string
	Message string
}

// newADOServer starts a server holding the given state. The caller sets the
// fields it cares about before making requests.
func newADOServer(t *testing.T) *adoServer {
	t.Helper()

	// Azure Pipelines variables of the machine running the tests must not leak
	// into a run. Clearing them here lets a test set the ones it wants afterwards.
	for _, key := range []string{
		"SYSTEM_TEAMPROJECT", "BUILD_REPOSITORY_NAME", "BUILD_SOURCEVERSION",
		"SYSTEM_PULLREQUEST_PULLREQUESTID", "AZURE_DEVOPS_EXT_PAT", "SYSTEM_ACCESSTOKEN",
	} {
		t.Setenv(key, "")
	}

	s := &adoServer{
		Project:              "Payments",
		Repository:           "checkout",
		Token:                clientToken,
		Tags:                 map[string]string{},
		AnnotatedTags:        map[string]string{},
		Branches:             map[string]string{},
		DefaultBranch:        "refs/heads/main",
		Labels:               map[int][]ServerLabel{},
		PullRequestForCommit: map[string]int{},
	}
	s.Server = httptest.NewServer(s)
	t.Cleanup(s.Close)
	return s
}

// ServeHTTP dispatches a request to the endpoint that handles it.
func (s *adoServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()

	// Every call has to carry the personal access token as basic auth.
	if user, token, ok := r.BasicAuth(); !ok || user != "" || token != s.Token {
		s.fail(w, http.StatusUnauthorized, "TF400813: The user is not authorized to access this resource.")
		return
	}

	if r.Method == http.MethodOptions && r.URL.Path == "/_apis" {
		s.writeCollection(w, apiResourceLocations())
		return
	}

	base := fmt.Sprintf("/%s/_apis/git/repositories/%s", s.Project, s.Repository)
	switch path := r.URL.Path; {
	case path == "/_apis/resourceAreas":
		// An empty list makes the SDK address the base URL directly, the way it
		// behaves against an on-premises server.
		s.writeCollection(w, []any{})
	case path == base+"/refs":
		s.serveRefs(w, r)
	case path == base+"/commits":
		s.serveCommits(w)
	case strings.HasPrefix(path, base+"/commits/"):
		s.serveCommit(w, strings.TrimPrefix(path, base+"/commits/"))
	case path == base+"/annotatedtags":
		s.serveCreateAnnotatedTag(w, r)
	case path == base+"/pullRequestQuery":
		s.servePullRequestQuery(w, r)
	case path == fmt.Sprintf("/%s/_apis/git/repositories/%s", s.Project, s.Repository):
		s.writeJSON(w, map[string]any{"name": s.Repository, "defaultBranch": s.DefaultBranch})
	case strings.HasPrefix(path, base+"/pullRequests/"):
		s.servePullRequestLabels(w, strings.TrimSuffix(strings.TrimPrefix(path, base+"/pullRequests/"), "/labels"))
	default:
		s.fail(w, http.StatusNotFound, "no route for "+path)
	}
}

// serveRefs lists refs, or creates one when a ref update is posted.
func (s *adoServer) serveRefs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.serveCreateLightweightTag(w, r)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var refs []map[string]any
	filter := r.URL.Query().Get("filter")
	if strings.HasPrefix(filter, "tags") {
		for name, commit := range s.Tags {
			ref := map[string]any{"name": "refs/tags/" + name, "objectId": commit}
			// An annotated tag's ref points at the tag object; the commit only
			// appears in the peeled ID.
			if peeled, ok := s.AnnotatedTags[name]; ok {
				ref["objectId"] = "tagobject-" + name
				ref["peeledObjectId"] = peeled
			}
			refs = append(refs, ref)
		}
	} else {
		for name, commit := range s.Branches {
			if strings.HasPrefix("heads/"+name, filter) {
				refs = append(refs, map[string]any{"name": "refs/heads/" + name, "objectId": commit})
			}
		}
	}
	sortRefs(refs)

	// Hand out one page at a time when paging is switched on.
	if size := s.TagPageSize; size > 0 && len(refs) > size {
		start := 0
		if token := r.URL.Query().Get("continuationToken"); token != "" {
			start, _ = strconv.Atoi(token)
		}
		end := start + size
		if end < len(refs) {
			w.Header().Set("x-ms-continuationtoken", strconv.Itoa(end))
		} else {
			end = len(refs)
		}
		refs = refs[start:end]
	}
	s.writeCollection(w, refs)
}

// serveCommits returns the commit range, truncating the messages Azure DevOps
// would truncate.
func (s *adoServer) serveCommits(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	commits := make([]map[string]any, 0, len(s.Commits))
	for _, commit := range s.Commits {
		entry := map[string]any{"commitId": commit.ID, "comment": commit.Message}
		if commit.TruncatedIn != "" {
			entry["comment"] = commit.TruncatedIn
			entry["commentTruncated"] = true
		}
		commits = append(commits, entry)
	}
	s.writeCollection(w, commits)
}

// serveCommit returns a single commit with its complete message.
func (s *adoServer) serveCommit(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, commit := range s.Commits {
		if commit.ID == id {
			s.writeJSON(w, map[string]any{"commitId": commit.ID, "comment": commit.Message})
			return
		}
	}
	s.fail(w, http.StatusNotFound, "no commit "+id)
}

// servePullRequestLabels returns the labels of a pull request.
func (s *adoServer) servePullRequestLabels(w http.ResponseWriter, rawID string) {
	id, err := strconv.Atoi(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "bad pull request id "+rawID)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	labels, ok := s.Labels[id]
	if !ok {
		s.fail(w, http.StatusNotFound, fmt.Sprintf("no pull request %d", id))
		return
	}
	out := make([]map[string]any, 0, len(labels))
	for _, label := range labels {
		out = append(out, map[string]any{"name": label.Name, "active": label.Active})
	}
	s.writeCollection(w, out)
}

// servePullRequestQuery answers the lookup of the pull request a commit was
// merged by.
func (s *adoServer) servePullRequestQuery(w http.ResponseWriter, r *http.Request) {
	var query struct {
		Queries []struct {
			Items []string `json:"items"`
			Type  string   `json:"type"`
		} `json:"queries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		s.fail(w, http.StatusBadRequest, "bad query: "+err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]map[string][]map[string]any, 0, len(query.Queries))
	for _, q := range query.Queries {
		found := map[string][]map[string]any{}
		for _, commit := range q.Items {
			if id, ok := s.PullRequestForCommit[commit]; ok {
				// Azure DevOps echoes the commit in upper case.
				found[strings.ToUpper(commit)] = []map[string]any{{"pullRequestId": id}}
			}
		}
		results = append(results, found)
	}
	s.writeJSON(w, map[string]any{"results": results})
}

// serveCreateAnnotatedTag records an annotated tag creation.
func (s *adoServer) serveCreateAnnotatedTag(w http.ResponseWriter, r *http.Request) {
	var tag struct {
		Name         string `json:"name"`
		Message      string `json:"message"`
		TaggedObject struct {
			ObjectID string `json:"objectId"`
		} `json:"taggedObject"`
	}
	if err := json.NewDecoder(r.Body).Decode(&tag); err != nil {
		s.fail(w, http.StatusBadRequest, "bad tag: "+err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Tags[tag.Name]; exists {
		s.fail(w, http.StatusConflict, "the tag "+tag.Name+" already exists")
		return
	}
	s.CreatedAnnotated = append(s.CreatedAnnotated, CreatedTag{
		Name: tag.Name, Commit: tag.TaggedObject.ObjectID, Message: tag.Message,
	})
	s.Tags[tag.Name] = tag.TaggedObject.ObjectID
	s.writeJSON(w, map[string]any{"name": tag.Name, "objectId": "tagobject-" + tag.Name})
}

// serveCreateLightweightTag records a ref update that creates a tag.
func (s *adoServer) serveCreateLightweightTag(w http.ResponseWriter, r *http.Request) {
	var updates []struct {
		Name        string `json:"name"`
		OldObjectID string `json:"oldObjectId"`
		NewObjectID string `json:"newObjectId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		s.fail(w, http.StatusBadRequest, "bad ref update: "+err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		name := strings.TrimPrefix(update.Name, "refs/tags/")
		if s.RejectTagCreation != "" {
			// A rejected update is reported with a 200 and success=false.
			results = append(results, map[string]any{
				"name":          update.Name,
				"success":       false,
				"updateStatus":  "rejectedByPolicy",
				"customMessage": s.RejectTagCreation,
			})
			continue
		}
		s.CreatedLightweight = append(s.CreatedLightweight, CreatedTag{Name: name, Commit: update.NewObjectID})
		s.Tags[name] = update.NewObjectID
		results = append(results, map[string]any{"name": update.Name, "success": true})
	}
	s.writeCollection(w, results)
}

// OrgURL is the organisation URL taggr is pointed at.
func (s *adoServer) OrgURL() string { return s.Server.URL }

// Created returns every tag the server accepted, annotated or lightweight.
func (s *adoServer) Created() []CreatedTag {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(append([]CreatedTag{}, s.CreatedAnnotated...), s.CreatedLightweight...)
}

// Called reports whether a request with the given method and path suffix arrived.
func (s *adoServer) Called(method, pathSuffix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, request := range s.Requests {
		if strings.HasPrefix(request, method+" ") && strings.HasSuffix(request, pathSuffix) {
			return true
		}
	}
	return false
}

func (s *adoServer) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// writeCollection wraps a list the way the Azure DevOps API does.
func (s *adoServer) writeCollection(w http.ResponseWriter, value any) {
	if value == nil {
		value = []any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"value": value, "count": lengthOf(value)})
}

func (s *adoServer) fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": message, "typeKey": "TestError"})
}

func lengthOf(value any) int {
	switch v := value.(type) {
	case []any:
		return len(v)
	case []map[string]any:
		return len(v)
	case []map[string][]map[string]any:
		return len(v)
	default:
		return 0
	}
}

// sortRefs keeps the listing order stable, which makes paging reproducible.
func sortRefs(refs []map[string]any) {
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && refs[j]["name"].(string) < refs[j-1]["name"].(string); j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

// apiResourceLocations is the endpoint catalogue the SDK fetches with an OPTIONS
// request before its first call. The IDs are the ones the SDK asks for; the route
// templates decide which paths it then builds.
func apiResourceLocations() []map[string]any {
	location := func(id, area, resource, template string) map[string]any {
		return map[string]any{
			"id":              id,
			"area":            area,
			"resourceName":    resource,
			"routeTemplate":   template,
			"minVersion":      "1.0",
			"maxVersion":      "7.1",
			"releasedVersion": "7.0",
			"resourceVersion": 1,
		}
	}
	const repo = "{project}/_apis/{area}/repositories/{repositoryId}"
	return []map[string]any{
		location("e81700f7-3be2-46de-8624-2eb35882fcaa", "location", "resourceAreas", "_apis/{resource}"),
		location("2d874a60-a811-4f62-9c9f-963a6ea0a55b", "git", "refs", repo+"/{resource}"),
		location("c2570c3b-5b3f-41b8-98bf-5407bfde8d58", "git", "commits", repo+"/{resource}/{commitId}"),
		location("225f7195-f9c7-4d14-ab28-a83f7ff77e1f", "git", "repositories", "{project}/_apis/{area}/{resource}/{repositoryId}"),
		location("5e8a8081-3851-4626-b677-9891cc04102e", "git", "annotatedtags", repo+"/{resource}"),
		location("f22387e3-984e-4c52-9c6d-fbb8f14c812d", "git", "labels", repo+"/pullRequests/{pullRequestId}/{resource}"),
		location("b3a6eebe-9cf0-49ea-b6cb-1a4c5f5007b0", "git", "pullRequestQuery", repo+"/{resource}"),
	}
}
