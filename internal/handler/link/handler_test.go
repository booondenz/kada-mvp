package link

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
)

func init() { gin.SetMode(gin.TestMode) }

const testUserID = int64(42)

type fakeLinkService struct {
	createRes *domain.LinkInfo
	createErr error

	getRes *domain.LinkInfo
	getErr error

	listRes *domain.PaginatedLinks
	listErr error

	updateRes *domain.LinkInfo
	updateErr error

	deleteErr error

	batchDeleted int64
	batchErr     error

	gotUserID       int64
	gotCreate       domain.CreateLinkRequest
	gotGetID        int64
	gotListArgs     [8]any
	gotUpdateID     int64
	gotUpdate       domain.UpdateLinkRequest
	gotDeleteID     int64
	gotBatchIDs     []int64
	gotBatchTagID   int64
	gotBatchTagUser int64
}

func (f *fakeLinkService) Create(_ context.Context, userID int64, req domain.CreateLinkRequest) (*domain.LinkInfo, error) {
	f.gotUserID, f.gotCreate = userID, req
	return f.createRes, f.createErr
}

func (f *fakeLinkService) GetByID(_ context.Context, linkID, userID int64) (*domain.LinkInfo, error) {
	f.gotGetID, f.gotUserID = linkID, userID
	return f.getRes, f.getErr
}

func (f *fakeLinkService) List(_ context.Context, userID int64, page, pageSize int, search string, folderID, tagID, workspaceID int64, sort string) (*domain.PaginatedLinks, error) {
	f.gotUserID = userID
	f.gotListArgs = [8]any{page, pageSize, search, folderID, tagID, workspaceID, sort, userID}
	return f.listRes, f.listErr
}

func (f *fakeLinkService) Update(_ context.Context, linkID, userID int64, req domain.UpdateLinkRequest) (*domain.LinkInfo, error) {
	f.gotUpdateID, f.gotUserID, f.gotUpdate = linkID, userID, req
	return f.updateRes, f.updateErr
}

func (f *fakeLinkService) Delete(_ context.Context, linkID, userID int64) error {
	f.gotDeleteID, f.gotUserID = linkID, userID
	return f.deleteErr
}

func (f *fakeLinkService) BatchDelete(_ context.Context, ids []int64, userID int64) (int64, error) {
	f.gotBatchIDs, f.gotUserID = ids, userID
	return f.batchDeleted, f.batchErr
}

func (f *fakeLinkService) BatchTag(_ context.Context, ids []int64, tagID, userID int64) error {
	f.gotBatchIDs, f.gotBatchTagID, f.gotBatchTagUser = ids, tagID, userID
	return f.batchErr
}

func newTestRouter(svc LinkService) *gin.Engine {
	r := gin.New()
	h := NewHandler(svc)
	authMW := func(c *gin.Context) { c.Set("user_id", testUserID); c.Next() }
	h.RegisterRoutes(r.Group("/api"), authMW)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var sampleLink = &domain.LinkInfo{
	ID:          7,
	ShortCode:   "abc1234",
	ShortURL:    "https://kada.link/r/abc1234",
	OriginalURL: "https://example.com/target",
	IsActive:    true,
	Tags:        []domain.LinkTagInfo{},
}

// TestCreate 对应练习 64。
func TestCreate(t *testing.T) {
	svc := &fakeLinkService{createRes: sampleLink}
	r := newTestRouter(svc)

	w := do(r, http.MethodPost, "/api/links", `{"original_url":"https://example.com/target"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d, want 201（这次请求创建了一个新资源）: %s", w.Code, w.Body.String())
	}
	if svc.gotUserID != testUserID {
		t.Errorf("服务收到的 user_id = %d, want %d（必须来自中间件，不能来自请求体）", svc.gotUserID, testUserID)
	}
	if svc.gotCreate.OriginalURL != "https://example.com/target" {
		t.Errorf("服务收到的 OriginalURL = %q", svc.gotCreate.OriginalURL)
	}
	var body struct {
		Link *domain.LinkInfo `json:"link"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body.Link == nil || body.Link.ShortCode != "abc1234" {
		t.Errorf("响应里没有 link: %s", w.Body.String())
	}
}

func TestCreateRejectsAnInvalidBody(t *testing.T) {
	r := newTestRouter(&fakeLinkService{})
	// original_url 是 required,url：这两条都该在 handler 这一层就被挡住。
	for _, body := range []string{`{}`, `{"original_url":"not-a-url"}`, `not json`} {
		if w := do(r, http.MethodPost, "/api/links", body); w.Code != http.StatusBadRequest {
			t.Errorf("body=%q 时状态码 = %d, want 400", body, w.Code)
		}
	}
}

func TestCreateReportsServiceFailure(t *testing.T) {
	r := newTestRouter(&fakeLinkService{createErr: errors.New("this short code is already taken")})
	w := do(r, http.MethodPost, "/api/links", `{"original_url":"https://example.com"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already taken") {
		t.Errorf("响应里没有服务给的错误信息: %s", w.Body.String())
	}
}

// TestGetReturns404ForALinkYouDoNotOwn 对应练习 66。
//
// 关键点：**不区分 403 和 404**。区分它们等于提供一个"探测别人有哪些 id"的接口。
func TestGetReturns404ForALinkYouDoNotOwn(t *testing.T) {
	r := newTestRouter(&fakeLinkService{getErr: domain.ErrLinkNotFound})
	if w := do(r, http.MethodGet, "/api/links/7", ""); w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, want 404", w.Code)
	}
}

func TestGetRejectsANonNumericID(t *testing.T) {
	r := newTestRouter(&fakeLinkService{})
	if w := do(r, http.MethodGet, "/api/links/abc", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", w.Code)
	}
}

func TestGetReturnsTheLink(t *testing.T) {
	svc := &fakeLinkService{getRes: sampleLink}
	r := newTestRouter(svc)

	w := do(r, http.MethodGet, "/api/links/7", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if svc.gotGetID != 7 || svc.gotUserID != testUserID {
		t.Errorf("服务收到 (linkID=%d, userID=%d), want (7, %d)", svc.gotGetID, svc.gotUserID, testUserID)
	}
}

// TestList 对应练习 65。
func TestList(t *testing.T) {
	svc := &fakeLinkService{listRes: &domain.PaginatedLinks{
		Links:      []domain.LinkInfo{*sampleLink},
		TotalCount: 1,
		Page:       2,
		PageSize:   10,
	}}
	r := newTestRouter(svc)

	w := do(r, http.MethodGet, "/api/links?page=2&page_size=10&search=kada&folder_id=3&tag_id=4&workspace_id=5&sort=clicks_desc", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}

	got := svc.gotListArgs
	if got[0] != 2 || got[1] != 10 {
		t.Errorf("分页参数 = %v, want page=2 page_size=10", got[:2])
	}
	if got[2] != "kada" {
		t.Errorf("search = %v, want \"kada\"", got[2])
	}
	if got[3] != int64(3) || got[4] != int64(4) || got[5] != int64(5) {
		t.Errorf("筛选参数 = %v, want folder=3 tag=4 workspace=5", got[3:6])
	}
	if got[6] != "clicks_desc" {
		t.Errorf("sort = %v, want \"clicks_desc\"", got[6])
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	for _, key := range []string{"links", "total_count", "page", "page_size"} {
		if _, ok := body[key]; !ok {
			t.Errorf("响应里缺少 %q 字段: %s", key, w.Body.String())
		}
	}
}

func TestListUsesDefaultsWhenParamsAreMissing(t *testing.T) {
	svc := &fakeLinkService{listRes: &domain.PaginatedLinks{}}
	r := newTestRouter(svc)

	if w := do(r, http.MethodGet, "/api/links", ""); w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if svc.gotListArgs[0] != 1 || svc.gotListArgs[1] != 20 {
		t.Errorf("默认分页 = %v, want page=1 page_size=20", svc.gotListArgs[:2])
	}
	if svc.gotListArgs[6] != "created_desc" {
		t.Errorf("默认排序 = %v, want \"created_desc\"", svc.gotListArgs[6])
	}
}

func TestUpdate(t *testing.T) {
	title := "新标题"
	svc := &fakeLinkService{updateRes: sampleLink}
	r := newTestRouter(svc)

	w := do(r, http.MethodPatch, "/api/links/7", `{"title":"`+title+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}
	if svc.gotUpdateID != 7 {
		t.Errorf("服务收到的 linkID = %d, want 7", svc.gotUpdateID)
	}
	if svc.gotUpdate.Title == nil || *svc.gotUpdate.Title != title {
		t.Errorf("服务收到的 Title = %v, want %q", svc.gotUpdate.Title, title)
	}
}

func TestDelete(t *testing.T) {
	svc := &fakeLinkService{}
	r := newTestRouter(svc)

	w := do(r, http.MethodDelete, "/api/links/7", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if svc.gotDeleteID != 7 || svc.gotUserID != testUserID {
		t.Errorf("服务收到 (%d, %d), want (7, %d)", svc.gotDeleteID, svc.gotUserID, testUserID)
	}
}

// TestBatchDeleteReportsTheRealCount 对应练习 69。
//
// 返回的必须是**实际删掉的行数**，不是请求里的 id 个数：
// 当其中一些不属于调用方时，这两个数字不一样，而前端要拿它做提示。
func TestBatchDeleteReportsTheRealCount(t *testing.T) {
	svc := &fakeLinkService{batchDeleted: 2}
	r := newTestRouter(svc)

	w := do(r, http.MethodPost, "/api/links/batch-delete", `{"ids":[1,2,3]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body.Deleted != 2 {
		t.Errorf("deleted = %d, want 2（实际删掉的行数，不是请求里的 3）", body.Deleted)
	}
	if len(svc.gotBatchIDs) != 3 {
		t.Errorf("服务收到的 ids = %v, want [1 2 3]", svc.gotBatchIDs)
	}
}

// 固定路径必须排在 /links/:id 之前，否则 "batch-delete" 会被当成一个 id。
// 这条测试盯的就是这个注册顺序。
func TestBatchRoutesAreNotSwallowedByTheIDRoute(t *testing.T) {
	svc := &fakeLinkService{batchDeleted: 1}
	r := newTestRouter(svc)

	if w := do(r, http.MethodPost, "/api/links/batch-delete", `{"ids":[1]}`); w.Code != http.StatusOK {
		t.Fatalf("batch-delete 状态码 = %d, want 200（不要被 /links/:id 吃掉）: %s", w.Code, w.Body.String())
	}
	if w := do(r, http.MethodPost, "/api/links/batch-tag", `{"ids":[1],"tag_id":5}`); w.Code != http.StatusOK {
		t.Fatalf("batch-tag 状态码 = %d, want 200", w.Code)
	}
	if svc.gotBatchTagID != 5 {
		t.Errorf("服务收到的 tagID = %d, want 5", svc.gotBatchTagID)
	}
}
