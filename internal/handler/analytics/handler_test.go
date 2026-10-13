package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
)

func init() { gin.SetMode(gin.TestMode) }

const testUserID = int64(42)

type fakeAnalyticsService struct {
	overview  domain.Overview
	overviewE error

	trend  []domain.TrendPoint
	trendE error

	platforms  []domain.PlatformCount
	platformsE error

	gotUserID int64
	gotDays   int
}

func (f *fakeAnalyticsService) Overview(_ context.Context, userID int64) (domain.Overview, error) {
	f.gotUserID = userID
	return f.overview, f.overviewE
}

func (f *fakeAnalyticsService) DailyTrend(_ context.Context, userID int64, days int) ([]domain.TrendPoint, error) {
	f.gotUserID, f.gotDays = userID, days
	return f.trend, f.trendE
}

func (f *fakeAnalyticsService) PlatformBreakdown(_ context.Context, userID int64) ([]domain.PlatformCount, error) {
	f.gotUserID = userID
	return f.platforms, f.platformsE
}

func newTestRouter(svc AnalyticsService) *gin.Engine {
	r := gin.New()
	h := NewHandler(svc)
	authMW := func(c *gin.Context) { c.Set("user_id", testUserID); c.Next() }
	h.RegisterRoutes(r.Group("/api"), authMW)
	return r
}

func doGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestOverview 对应练习 74。
func TestOverview(t *testing.T) {
	svc := &fakeAnalyticsService{overview: domain.Overview{TotalLinks: 12, TotalClicks: 340}}
	r := newTestRouter(svc)

	w := doGet(r, "/api/analytics/overview")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}
	if svc.gotUserID != testUserID {
		t.Errorf("服务收到的 user_id = %d, want %d", svc.gotUserID, testUserID)
	}

	var body map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body["total_links"] != 12 || body["total_clicks"] != 340 {
		t.Errorf("响应 = %v, want total_links=12 total_clicks=340", body)
	}
}

func TestOverviewReportsFailure(t *testing.T) {
	r := newTestRouter(&fakeAnalyticsService{overviewE: errors.New("db down")})
	if w := doGet(r, "/api/analytics/overview"); w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, want 500", w.Code)
	}
}

// TestTrend 对应练习 75。
func TestTrend(t *testing.T) {
	svc := &fakeAnalyticsService{trend: []domain.TrendPoint{
		{Day: "2024-03-01", Clicks: 3},
		{Day: "2024-03-02", Clicks: 0},
	}}
	r := newTestRouter(svc)

	w := doGet(r, "/api/analytics/trend?days=7")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}
	if svc.gotDays != 7 {
		t.Errorf("服务收到的 days = %d, want 7", svc.gotDays)
	}

	var body struct {
		Points []domain.TrendPoint `json:"points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if len(body.Points) != 2 {
		t.Fatalf("points 长度 = %d, want 2: %s", len(body.Points), w.Body.String())
	}
	if body.Points[1].Clicks != 0 {
		t.Errorf("第二个点的 clicks = %d, want 0（没有点击的日期也要出现）", body.Points[1].Clicks)
	}
}

func TestTrendDefaultsToThirtyDays(t *testing.T) {
	svc := &fakeAnalyticsService{}
	r := newTestRouter(svc)

	if w := doGet(r, "/api/analytics/trend"); w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if svc.gotDays != 30 {
		t.Errorf("默认 days = %d, want 30", svc.gotDays)
	}
}

// 一个坏参数不该变成 500：把 Atoi 的错误忽略掉，退化成默认值即可。
func TestTrendToleratesABadDaysParam(t *testing.T) {
	svc := &fakeAnalyticsService{}
	r := newTestRouter(svc)

	if w := doGet(r, "/api/analytics/trend?days=abc"); w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（坏的 query 参数不该让接口 500）", w.Code)
	}
}

// TestPlatforms 对应练习 76。
func TestPlatforms(t *testing.T) {
	svc := &fakeAnalyticsService{platforms: []domain.PlatformCount{
		{Platform: "browser", Clicks: 100},
		{Platform: "wechat", Clicks: 40},
	}}
	r := newTestRouter(svc)

	w := doGet(r, "/api/analytics/platforms")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}

	var body struct {
		Platforms []domain.PlatformCount `json:"platforms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if len(body.Platforms) != 2 || body.Platforms[0].Platform != "browser" {
		t.Errorf("platforms = %v", body.Platforms)
	}
}
