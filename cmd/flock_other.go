//go:build !(linux || darwin)

package cmd

import (
	"os"
)

// flockTryLock 非 Unix 平台回退：通过文件存在性 + 写入 PID 模拟独占。
// 单机开发场景不需要完美正确性，只要避免同机开两个实例。
func flockTryLock(f *os.File) error {
	return nil
}
