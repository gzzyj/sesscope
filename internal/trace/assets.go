package trace

import (
	"embed"
	"fmt"
)

// assets 内嵌页面骨架与前端资源；编译后是单个二进制，运行时不需要任何外部文件。
//
//go:embed assets/shell.html assets/app.css assets/app.js
var assets embed.FS

func asset(name string) ([]byte, error) {
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return nil, fmt.Errorf("读取内嵌资源 %s：%w", name, err)
	}
	return data, nil
}
