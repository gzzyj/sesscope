package cli

import "os"

// osUserHomeDir 单独一层，便于测试替换（当前直接转发）。
func osUserHomeDir() (string, error) { return os.UserHomeDir() }
