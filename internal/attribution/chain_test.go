package attribution

import (
	"testing"
)

// 复原用户给的真实形态：编码坏了的 CLI 失败 → 探目录 → 读脚本 → 重建环境 → 建目录 → write 才收工。
func TestCompensationChainStopsAtRealWork(t *testing.T) {
	fail := tool("bash", "completed", "{\"command\":\"tools\\\\fastdata.cmd auth status\"}",
		"ϵͳ�Ҳ���ָ����·����", "", 1000, 1100)
	probe := tool("bash", "completed", "{\"command\":\"Get-ChildItem -LiteralPath .fastdata-agent-kit\"}",
		"bin\nruntimes", "", 2000, 2100)
	readScript := tool("read", "completed", "{\"filePath\":\"/work/tools/fastdata.cmd\"}", "script body", "", 3000, 3100)
	envSetup := tool("bash", "completed", "{\"command\":\"$env:FASTDATA_KIT_HOME = $kit; fastdata auth status\"}",
		"{\"ok\":true}", "", 4000, 4100)
	grepSource := tool("grep", "completed", "{\"path\":\"/kit/venv/Lib/site-packages/fastdata_cli\"}",
		"match", "", 5000, 5100)
	prepare := tool("bash", "completed", "{\"command\":\"New-Item -ItemType Directory -Path outputs\"}",
		"ok", "", 6000, 6100)
	writeReport := tool("write", "completed", "{\"filePath\":\"/work/outputs/report.md\"}", "written", "", 7000, 7100)

	data := oneTurn(
		step(1, fail),
		step(2, probe),
		step(3, readScript),
		step(4, envSetup),
		step(5, grepSource),
		step(6, prepare),
		step(7, writeReport),
	)
	episodes := Analyze(data)
	chains := BuildChains(data, episodes, DefaultChainGap)
	if len(chains) != 1 {
		t.Fatalf("期望 1 条补偿链，实际 %d", len(chains))
	}
	chain := chains[0]
	if chain.StartStep != 1 {
		t.Errorf("链起点 = %d，期望 1", chain.StartStep)
	}
	if chain.EndStep != 6 {
		t.Errorf("链终点 = %d，期望 6（第 7 步 write 是真正开工，不能算进补偿）", chain.EndStep)
	}
	if chain.Steps != 6 {
		t.Errorf("步数 = %d，期望 6", chain.Steps)
	}
	if chain.Code != ChainAuthLogin {
		t.Errorf("code = %s，期望 auth_login_compensation（命令里有 auth）", chain.Code)
	}
	if chain.Outcome == "" {
		t.Error("outcome 不应为空")
	}
}

func TestChainDetectsBoundaryAtWrite(t *testing.T) {
	// 命令里必须有一个可识别的主题词（脚本名/环境变量/隐藏目录），否则无法判断补偿范围
	fail := tool("bash", "error", "{\"command\":\"tools\\\\cli.cmd run\"}", "", "boom", 1000, 1100)
	data := oneTurn(step(1, fail), step(2, tool("write", "completed", "{\"filePath\":\"/a.txt\"}", "ok", "", 2000, 2100)))
	chains := BuildChains(data, Analyze(data), DefaultChainGap)
	if len(chains) != 1 || chains[0].Steps != 1 {
		t.Fatalf("期望只有 1 步的链（下一步就是 write），实际 %+v", chains)
	}
}
