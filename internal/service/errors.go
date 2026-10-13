package service

import (
	"errors"

	"gorm.io/gorm"
)

// isDuplicateKey 报告这个错误是不是唯一约束冲突。
//
// gorm.Config{TranslateError: true}（见 infra.NewDB）会把 PostgreSQL 的 SQLSTATE 23505
// 包成 gorm.ErrDuplicatedKey，同时**保留驱动原始错误在错误链里**，
// 所以无论翻译有没有发生，errors.Is 都能匹配到。
//
// 这一份是**已给**的。
func isDuplicateKey(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
