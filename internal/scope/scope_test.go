package scope

import (
	"testing"

	"grscan/internal/state"
	"grscan/internal/util"
)

func mkRepo(path, name, group string, tags ...string) *state.Repo {
	return &state.Repo{
		ID:          util.NormalizePath(path),
		Path:        path,
		Name:        name,
		Group:       group,
		Tags:        tags,
		HasUpstream: true,
	}
}

func fixture() *state.State {
	return &state.State{Repos: []*state.Repo{
		mkRepo(`D:\repos\foo`, "foo", "工作", "待推送"),
		mkRepo(`D:\repos\bar`, "bar", "工作"),
		mkRepo(`D:\work\baz`, "baz", "", "待推送"),
	}}
}

func mustParse(t *testing.T, s string) Scope {
	t.Helper()
	sc, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q) 出错: %v", s, err)
	}
	return sc
}

func TestParseStringRoundTrip(t *testing.T) {
	cases := []string{
		"tag:待推送",
		"tag:待推送/foo",
		"group:工作",
		"group:工作/tag:待推送",
		`file:D:\repos`,
		`file:D:\repos\foo`,
		`file:D:\repos/tag:待推送`,
	}
	for _, in := range cases {
		got := mustParse(t, in).String()
		if got != in {
			t.Errorf("往返不一致: 输入 %q 输出 %q", in, got)
		}
	}
}

func TestParseNormalizesBashStylePath(t *testing.T) {
	// Git Bash 风格的 / 属于路径，不该被当成段分隔符撕裂
	sc := mustParse(t, "file:D:/repos/foo")
	if got := sc.String(); got != `file:D:\repos\foo` {
		t.Errorf("路径没规范化: %q", got)
	}
	if len(sc.Segs) != 1 {
		t.Fatalf("被切成 %d 段了: %+v", len(sc.Segs), sc.Segs)
	}
}

func TestParseAcceptsBothSeparators(t *testing.T) {
	a := mustParse(t, "tag:待推送")
	b := mustParse(t, "tag#待推送")
	if a.String() != b.String() {
		t.Errorf("两种分隔符解析结果不同: %q vs %q", a.String(), b.String())
	}
	if a.String() != "tag:待推送" {
		t.Errorf("输出没统一成冒号: %q", a.String())
	}
}

func TestParseRejectsEmptyIdent(t *testing.T) {
	if _, err := Parse("tag:"); err == nil {
		t.Error("空标识应该报错")
	}
}

func TestParent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tag:待推送/foo", "tag:待推送"}, // 段内没级可降 → 去整段
		{"tag:待推送", ""},
		{`file:D:\repos\foo`, `file:D:\repos`}, // 段内降一级
		{`file:D:\repos`, ""},                  // 再上是盘符，不算一级 → 去整段
		{`file:D:\`, ""},
		{"group:工作/tag:待推送", "group:工作"},
	}
	for _, c := range cases {
		got := mustParse(t, c.in).Parent().String()
		if got != c.want {
			t.Errorf("Parent(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}

func TestCrossDimensionIsIntersection(t *testing.T) {
	st := fixture()
	res, err := Resolve(mustParse(t, "group:工作/tag:待推送"), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Name != "foo" {
		t.Fatalf("交集算错，得到 %d 个: %+v", len(res.Repos), names(res.Repos))
	}
	// 反过来：先 tag 再 group，结果应当一样
	res2, err := Resolve(mustParse(t, "tag:待推送/group:工作"), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if len(res2.Repos) != 1 || res2.Repos[0].Name != "foo" {
		t.Fatalf("反序交集算错: %+v", names(res2.Repos))
	}
}

func TestSameDimensionDrillsDown(t *testing.T) {
	st := fixture()
	res, err := Resolve(mustParse(t, `file:D:\repos`), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindCollection || len(res.Repos) != 2 {
		t.Fatalf("下钻算错: kind=%s %+v", res.Kind, names(res.Repos))
	}
}

func TestRepoNodeDerivedFromFileSeg(t *testing.T) {
	st := fixture()
	res, err := Resolve(mustParse(t, `file:D:\repos\foo`), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindRepo {
		t.Fatalf("foo 本身是仓库，应推导出仓库节点，实际是 %s", res.Kind)
	}
	if len(res.Repos) != 1 {
		t.Fatalf("仓库节点应当只有一个仓库，实际 %d 个", len(res.Repos))
	}
}

func TestRepoNodeByName(t *testing.T) {
	st := fixture()
	res, err := Resolve(mustParse(t, "tag:待推送/foo"), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindRepo || res.Repos[0].Name != "foo" {
		t.Fatalf("按名字进仓库失败: kind=%s", res.Kind)
	}
}

func TestResolveReportsMissing(t *testing.T) {
	st := fixture()
	if _, err := Resolve(mustParse(t, "tag:不存在的标签"), st); err == nil {
		t.Error("匹配不到应当报错，绝不静默退回全局")
	}
}

func TestRootChildrenHaveThreeFacets(t *testing.T) {
	st := fixture()
	res, err := Resolve(Scope{Source: SourceRoot}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindRoot {
		t.Fatalf("空 scope 应当是根，实际 %s", res.Kind)
	}
	seen := map[string]int{}
	for _, it := range res.Children {
		seen[it.Facet]++
		if it.Count <= 0 {
			t.Errorf("子项 %q 没有数量", it.Label)
		}
	}
	for _, d := range []string{DimFile, DimGroup, DimTag} {
		if seen[d] == 0 {
			t.Errorf("根下缺少 %s 维度入口", d)
		}
	}
}

// 嵌套仓库：一个目录自己就是仓库，里面还套着别的仓库。
// 这是 scope 最容易出漏洞的地方 —— 不指定维度就无法确定看哪个视角。
func nestedFixture() *state.State {
	return &state.State{Repos: []*state.Repo{
		mkRepo(`D:\repos\opensource`, "opensource", ""),
		mkRepo(`D:\repos\opensource\a`, "a", ""),
		mkRepo(`D:\repos\opensource\b`, "b", ""),
	}}
}

func TestNestedRepoIsCollectionWithSelf(t *testing.T) {
	st := nestedFixture()
	res, err := Resolve(mustParse(t, `file:D:\repos\opensource`), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindCollection {
		t.Fatalf("自身是仓库且内含子仓库时应当是集合，实际 %s", res.Kind)
	}
	if res.Self == nil || res.Self.Name != "opensource" {
		t.Fatalf("Self 没认出来: %v", res.Self)
	}
	// 集合里只有子仓库，自身被摘出来单独报，否则它会被统计两次
	got := names(res.Repos)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("集合内容不对: %v", got)
	}
	if res.Totals.Repos != 2 {
		t.Fatalf("总数应是 2，实际 %d", res.Totals.Repos)
	}
	// 子项里不能出现指向自己的那一项
	for _, it := range res.Children {
		if it.Label == "opensource" {
			t.Fatalf("子项里出现了自己: %+v", it)
		}
	}
}

func TestRepoPrefixLooksAtItselfOnly(t *testing.T) {
	st := nestedFixture()
	// repo: 明确表示"看它自己"，连完整路径也要认
	for _, s := range []string{`repo:D:\repos\opensource`, `repo:opensource`} {
		res, err := Resolve(mustParse(t, s), st)
		if err != nil {
			t.Fatalf("Resolve(%q) 出错: %v", s, err)
		}
		if res.Kind != KindRepo {
			t.Fatalf("%s 应当是仓库节点，实际 %s", s, res.Kind)
		}
		if len(res.Repos) != 1 || res.Repos[0].Name != "opensource" {
			t.Fatalf("%s 应只含它自己，实际 %v", s, names(res.Repos))
		}
	}
}

func TestPlainRepoDirIsStillRepoNode(t *testing.T) {
	// 目录里只有它自己（没嵌套）时不该退化成集合，否则 cd 进去看不到仓库界面
	st := fixture()
	res, err := Resolve(mustParse(t, `file:D:\repos\foo`), st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Kind != KindRepo || len(res.Repos) != 1 {
		t.Fatalf("单个仓库目录应当是仓库节点: kind=%s repos=%v", res.Kind, names(res.Repos))
	}
}

func names(in []*state.Repo) []string {
	out := make([]string, 0, len(in))
	for _, r := range in {
		out = append(out, r.Name)
	}
	return out
}

func TestTrailAndNodeType(t *testing.T) {
	st := fixture()

	// 1. Root
	rootSc := mustParse(t, "")
	if got := rootSc.TrailString(); got != "/" {
		t.Fatalf("根 TrailString() = %q, 期望 /", got)
	}
	resRoot, err := Resolve(rootSc, st)
	if err != nil {
		t.Fatal(err)
	}
	if resRoot.NodeType != NodeRoot {
		t.Fatalf("根 NodeType = %v, 期望 %v", resRoot.NodeType, NodeRoot)
	}

	// 2. Group
	grpSc := mustParse(t, "group:工作")
	if got := grpSc.TrailString(); got != "/ -> group:工作" {
		t.Fatalf("Group TrailString() = %q", got)
	}
	resGrp, err := Resolve(grpSc, st)
	if err != nil {
		t.Fatal(err)
	}
	if resGrp.NodeType != NodeGroup {
		t.Fatalf("Group NodeType = %v, 期望 %v", resGrp.NodeType, NodeGroup)
	}

	// 3. Repo inside Group
	repoSc := mustParse(t, "group:工作/foo")
	if got := repoSc.TrailString(); got != "/ -> group:工作 -> foo" {
		t.Fatalf("Repo TrailString() = %q", got)
	}
	resRepo, err := Resolve(repoSc, st)
	if err != nil {
		t.Fatal(err)
	}
	if resRepo.NodeType != NodeRepo {
		t.Fatalf("Repo NodeType = %v, 期望 %v", resRepo.NodeType, NodeRepo)
	}
	if resRepo.TargetRepo() == nil || resRepo.TargetRepo().Name != "foo" {
		t.Fatalf("TargetRepo() 错误: %+v", resRepo.TargetRepo())
	}
}

func TestEmptyGroupScope(t *testing.T) {
	st := fixture()
	if !st.AddGroup("study") {
		t.Fatal("AddGroup(study) 失败")
	}

	// 1. 根节点下应能看到空分组 study，且数量为 0
	rootSc := mustParse(t, "")
	resRoot, err := Resolve(rootSc, st)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range resRoot.Children {
		if it.Facet == DimGroup && it.Label == "group:study" {
			found = true
			if it.Count != 0 {
				t.Fatalf("期望 study 分组数量为 0，实际 %d", it.Count)
			}
		}
	}
	if !found {
		t.Fatal("根节点子项未包含新创建的空分组 group:study")
	}

	// 2. 能够正常进入空分组作用域
	studySc := mustParse(t, "group:study")
	resStudy, err := Resolve(studySc, st)
	if err != nil {
		t.Fatalf("进入空分组失败: %v", err)
	}
	if resStudy.NodeType != NodeGroup {
		t.Fatalf("NodeType 期望 %v，实际 %v", NodeGroup, resStudy.NodeType)
	}
	if len(resStudy.Repos) != 0 || resStudy.Totals.Repos != 0 {
		t.Fatalf("空分组仓库数量应为 0，实际 %d", resStudy.Totals.Repos)
	}

	// 3. 不存在的分组应当报错
	badSc := mustParse(t, "group:ghost")
	if _, err := Resolve(badSc, st); err == nil {
		t.Fatal("不存在的分组应当返回错误，但实际没有报错")
	}
}

func TestSummaryWithGroupAndTags(t *testing.T) {
	r1 := &state.Repo{
		Name:   "AgyArchiveHelper",
		Branch: "master",
		Group:  "leobert",
		Tags:   []string{"backup", "helper"},
	}
	s1 := Summary(r1)
	if s1 != "master · [leobert] · #backup #helper" {
		t.Fatalf("Summary 格式不符合预期: %q", s1)
	}

	r2 := &state.Repo{
		Name:   "GitSyncer",
		Branch: "main",
		Dirty:  true,
		Group:  "job",
		Tags:   []string{"sync"},
	}
	s2 := Summary(r2)
	if s2 != "main · 脏 · [job] · #sync" {
		t.Fatalf("Summary 格式不符合预期: %q", s2)
	}

	r3 := &state.Repo{
		Name:   "PinsLogParser",
		Branch: "dev",
		Ahead:  2,
		Group:  "study",
	}
	s3 := Summary(r3)
	if s3 != "dev · 领先 2 · [study]" {
		t.Fatalf("Summary 格式不符合预期: %q", s3)
	}

	r4 := &state.Repo{
		Name:   "DSP_learning",
		Branch: "master",
		Tags:   []string{"dsp"},
	}
	s4 := Summary(r4)
	if s4 != "master · #dsp" {
		t.Fatalf("Summary 格式不符合预期: %q", s4)
	}

	r5 := &state.Repo{
		Name:   "FileManager",
		Branch: "master",
	}
	s5 := Summary(r5)
	if s5 != "master" {
		t.Fatalf("Summary 格式不符合预期: %q", s5)
	}
}
