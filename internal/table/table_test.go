package table

import "testing"

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"abc", 3},
		{"中文", 4},
		{"a中b", 4},
		{"→", 1},
		{"", 0},
		{"🎯", 2},
		{"\t", 0},
	}
	for _, testCase := range cases {
		if got := DisplayWidth(testCase.text); got != testCase.want {
			t.Errorf("DisplayWidth(%q) = %d，期望 %d", testCase.text, got, testCase.want)
		}
	}
}

func TestRenderAlignsCJK(t *testing.T) {
	got := Render(
		[]Column{{Header: "名称"}, {Header: "数量", Align: Right}},
		[][]string{
			{"中文标题", "1"},
			{"ab", "22"},
		},
	)
	want := "" +
		"名称      数量\n" +
		"--------  ----\n" +
		"中文标题     1\n" +
		"ab          22\n"
	if got != want {
		t.Errorf("表格渲染不对：\n得到：\n%s\n期望：\n%s", got, want)
	}
}

func TestRenderPadsShortRows(t *testing.T) {
	got := Render(
		[]Column{{Header: "A"}, {Header: "B"}},
		[][]string{{"1"}},
	)
	want := "A  B\n-  -\n1\n"
	if got != want {
		t.Errorf("缺列的行应留白：\n%s", got)
	}
}
