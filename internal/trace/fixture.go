package trace

// InjectToolError 把第一个已完成的工具调用改成失败，用来验证页面上的红色报错路径。
// 只改内存里的数据，不动输入文件；生成的页面标题会带「（模拟数据）」。
func InjectToolError(data *Data) {
	injected := false
	for stepIndex := range data.Turns {
		for toolIndex := range data.Turns[stepIndex].Steps {
			step := &data.Turns[stepIndex].Steps[toolIndex]
			for index := range step.Tools {
				tool := &step.Tools[index]
				if injected || tool.Status != "completed" {
					continue
				}
				message := "SyntheticToolError: fixture —— 这条不是真实会话数据，只用于验证报错红色渲染。"
				tool.Status = "error"
				tool.ErrorText = &message
				step.Status = "error"
				injected = true
			}
		}
	}

	title := "（模拟数据）"
	if data.Meta.Title != nil {
		title += *data.Meta.Title
	} else {
		title += "opencode 会话"
	}
	data.Meta.Title = &title

	data.Warnings = append([]Warning{{
		Level:  "warn",
		Code:   "fixture",
		Title:  "这是模拟数据（-fixture-tool-error）",
		Detail: "第一个工具调用被强行改成 status=error，用来验证红色报错渲染；不含真实错误。",
	}}, data.Warnings...)

	// 注入后重算受影响的计数，避免页头的「报错」数字和台账对不上。
	recount(data)
}

func recount(data *Data) {
	errors, errorTools, running, toolRunning := 0, 0, 0, 0
	for _, turn := range data.Turns {
		for _, step := range turn.Steps {
			switch step.Status {
			case "error":
				errors++
			case "running":
				running++
			}
			for _, tool := range step.Tools {
				switch tool.Status {
				case "error":
					errorTools++
				case "running":
					toolRunning++
				}
			}
		}
	}
	data.Stats.Errors = errors
	data.Stats.ErrorTools = errorTools
	data.Stats.Running = running
	data.Stats.ToolRunning = toolRunning
}
