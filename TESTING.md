# 解码器模糊测试与回归语料说明

针对 WAV（`InspectAudio`）与 Y4M（`InspectVideo` / `NormalizeY4M`）两个真实
解码边界建立的可重复测试体系。目标：发现**格式声明与实际样本/画面不一致**时的
误接收、崩溃与资源失控，同时保证有效素材经接收 → 分析 → 导出后内容含义不变。

## 目录结构

```text
internal/media/
├── testbuild_test.go          # 字节级 WAV/Y4M 语料构造器（可表达畸形文件）
├── wav_regression_test.go     # WAV 表驱动回归语料 + 描述符/往返不变量
├── y4m_regression_test.go     # Y4M 表驱动回归语料 + 时长/往返不变量
├── fuzz_test.go               # FuzzInspectAudio / FuzzInspectVideo + 种子
├── pipeline_test.go           # 接收→分析→对齐→导出全链路含义保持 + 资源护栏
├── defect_evidence_test.go    # build tag: defectevidence，严格断言（不迁就实现）
├── defect_evidence_gen.go     # //go:build ignore，生成最小二进制证据
└── testdata/fuzz/             # 已固化的模糊语料（随仓库重放，无需缓存）
    ├── FuzzInspectAudio/      # 100 个输入（含手工边界种子与引擎探索样本）
    └── FuzzInspectVideo/      # 26 个输入
defects/
├── DEFECTS.md                 # 10 个生产缺陷的完整报告
└── evidence/                  # 每个缺陷一个最小二进制证据（46–826 字节）
cmd/defectverify/main.go       # 对证据文件跑真实解码器，输出 spec vs 观察裁决
test/fuzz/run.sh               # regression / fuzz / defects / evidence 入口
```

## 运行方式

```bash
# 常规回归（默认测试集，必须全绿；证据测试由 build tag 隔离，不参与其中）
go test ./... -count=1

# 带竞态检测的媒体边界回归
go test ./internal/media/ -race -count=1

# 仅重放全部模糊语料（代码种子 + testdata 固化语料）
go test ./internal/media/ -run 'FuzzInspect' -v -count=1

# 在线模糊（每个目标 60 秒，可传秒数）；新发现自动最小化并写入 testdata
test/fuzz/run.sh fuzz 60

# 严格 spec 断言证据集（缺陷未修复时按预期 FAIL，禁止修改断言迁就实现）
go test -tags defectevidence ./internal/media/ -run TestDefect -v

# 最小证据 + 真实解码器裁决对照（缺陷未修复时退出码 1）
go run ./cmd/defectverify

# 重新生成证据二进制（生产代码未改，输出应字节稳定）
go run ./internal/media/defect_evidence_gen.go
```

## 测试预言机（oracle）设计

### 模糊目标的通用不变量

1. **无崩溃**：任意字节流不得触发 panic（Go 原生 fuzz 自动捕获）。
2. **裁决确定性**：同一输入解码两次，接受/拒绝结论必须一致。
3. **接受即自洽**：接受文件的采样率/声道/位深/帧数/帧率/几何全部落在策略区间，
   样本数与 data 块字节、帧数与平面尺寸严格对账。
4. **描述符即字节**：`Digest` 与 `ByteCount` 必须等于输入字节的 SHA-256 与长度。
5. **往返保持含义**：接受的流重新编码后必须再次被接受，且采样率/声道/样本数、
   几何/帧数/每个平面字节逐项不变。

### 全链路含义保持（`pipeline_test.go`）

校准素材经 `AnalyzeSync → AlignAudio / NormalizeY4M → BuildReviewBundle`：

- 音频对齐是**精确的样本级移位**，逐样本核对 `out[i] == in[i+shift]`，
  仅允许边缘零填充；零偏移对齐必须逐样本还原原 PCM；
- 视频 conform 后几何、帧率、帧数与每一帧平面字节全部相等；
- ZIP 内三个条目与生产函数返回的字节逐一相等，收据摘要与产物一致。

### 资源失控护栏（`TestResourceBoundedness`）

- 近 4 GiB 的 RIFF/块声明 + 极小实际载荷：必须按实际字节拒绝，不得按声明分配；
- 1920×1080 几何但帧体仅 64 字节：在越界切片前拒绝；
- 帧数 1200 接受 / 1201 拒绝的边界固定；
- 模糊工作器按 `MaxAudioBytes`/`MaxVideoBytes` 预算跳过超大输入，
  与 HTTP 准入层的 8 MiB / 24 MiB 上限对齐。

## 缺陷处理纪律

- 生产代码保持原样；缺陷由 `defects/DEFECTS.md` + 最小证据 +
  tagged 测试三处独立跟踪。
- tagged 断言写的是 **spec 应有行为**。修复生产代码后相应用例自然转 PASS，
  再提升进常规回归语料；任何时候都不允许把断言改成接受当前错误行为。
- 模糊器若在未来暴露新失败，Go 会把最小化输入写入
  `internal/media/testdata/fuzz/<target>/`，随提交永久重放。
