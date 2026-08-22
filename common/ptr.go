package common

// 指针解引用辅助函数：nil 时返回默认值。
// 用于 GORM 模型里大量 *int / *int64 / *uint / *string 字段读取。

func DerefIntOr(p *int, dft int) int {
	if p == nil {
		return dft
	}
	return *p
}

func DerefInt64Or(p *int64, dft int64) int64 {
	if p == nil {
		return dft
	}
	return *p
}

func DerefUintOr(p *uint, dft uint) uint {
	if p == nil {
		return dft
	}
	return *p
}

func DerefStringOr(p *string, dft string) string {
	if p == nil {
		return dft
	}
	return *p
}
