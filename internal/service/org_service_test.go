package service

import (
	"context"
	"strings"
	"testing"
)

// TestValidateSlug 对应练习 62。
func TestValidateSlug(t *testing.T) {
	cases := []struct {
		slug    string
		wantErr bool
	}{
		{"abc", false},
		{"my-team", false},
		{"team2024", false},
		{"a1b", false},
		{"ab", true},                     // 太短
		{"My-Team", true},                // 大写
		{"-team", true},                  // 不能以连字符开头
		{"1team", true},                  // 不能以数字开头
		{"my_team", true},                // 下划线不在允许集合里
		{"", true},                       //
		{strings.Repeat("a", 51), true},  // 太长
		{strings.Repeat("a", 50), false}, // 刚好上限
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			err := validateSlug(tc.slug)
			if tc.wantErr && err == nil {
				t.Errorf("validateSlug(%q) = nil, want error", tc.slug)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateSlug(%q) = %v, want nil", tc.slug, err)
			}
		})
	}
}

// 三个组织类服务的列表查询都必须把 user_id 放进 WHERE。
//
// 这类查询写错的代价很具体：别人的文件夹/标签出现在你的列表里，
// 而"列表"通常被当成一个无害的读接口，最容易被漏掉归属检查。
func TestOrganizationListsAreScopedToTheUser(t *testing.T) {
	ctx := context.Background()

	t.Run("文件夹", func(t *testing.T) {
		db := newDryRunDB(t)
		stmts := captureSQL(t, db)
		if _, err := NewFolderService(db).List(ctx, 42); err != nil {
			t.Fatalf("FolderService.List 返回错误: %v", err)
		}
		sql := strings.Join(*stmts, "\n")
		if sql == "" {
			t.Fatal("List 没有发出任何 SQL")
		}
		containsAll(t, sql, "folders", "user_id")
	})

	t.Run("标签", func(t *testing.T) {
		db := newDryRunDB(t)
		stmts := captureSQL(t, db)
		if _, err := NewTagService(db).List(ctx, 42); err != nil {
			t.Fatalf("TagService.List 返回错误: %v", err)
		}
		containsAll(t, strings.Join(*stmts, "\n"), "tags", "user_id")
	})

	t.Run("工作区", func(t *testing.T) {
		db := newDryRunDB(t)
		stmts := captureSQL(t, db)
		if _, err := NewWorkspaceService(db).List(ctx, 42); err != nil {
			t.Fatalf("WorkspaceService.List 返回错误: %v", err)
		}
		containsAll(t, strings.Join(*stmts, "\n"), "workspaces", "user_id")
	})
}

// 标签列表必须是空切片而不是 nil：JSON 里 null 和 [] 对前端是两种不同的东西。
func TestTagListReturnsEmptySliceNotNil(t *testing.T) {
	db := newDryRunDB(t)
	tags, err := NewTagService(db).List(context.Background(), 42)
	if err != nil {
		t.Fatalf("TagService.List 返回错误: %v", err)
	}
	if tags == nil {
		t.Error("List 返回了 nil；没有标签时应该是空切片（否则 JSON 里是 null）")
	}
}
