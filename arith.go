package rle

import "math/bits"

// mulInt64 返回 a*b；乘积溢出 int64 时 ok=false。
//
// 用 bits.Mul64 求无符号 128 位积，再按有符号因子做高位修正
// （每个负因子减去另一因子的 2^64 倍），最后检查高位是否为低位
// 的符号扩展。直接用除法回代在 MinInt64 * -1 处会漏判，故不用。
func mulInt64(a, b int64) (int64, bool) {
	x, y := uint64(a), uint64(b)
	hi, lo := bits.Mul64(x, y)
	if a < 0 {
		hi -= y
	}
	if b < 0 {
		hi -= x
	}
	signExt := uint64(0)
	if lo&(1<<63) != 0 {
		signExt = ^uint64(0)
	}
	return int64(lo), hi == signExt
}

// addInt64 返回 a+b，溢出 int64 时 ok=false。
func addInt64(a, b int64) (int64, bool) {
	s := a + b
	if (a > 0 && b > 0 && s <= 0) || (a < 0 && b < 0 && s >= 0) {
		return 0, false
	}
	return s, true
}
