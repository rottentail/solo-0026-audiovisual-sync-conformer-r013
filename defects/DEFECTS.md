# 解码器生产缺陷报告（WAV / Y4M 边界）

本报告记录在为 `internal/media` 的 WAV（`InspectAudio`）与 Y4M
（`InspectVideo` / `NormalizeY4M`）真实解码边界建立模糊测试与回归语料的过程中
发现的生产缺陷。所有断言均依据 `PROJECT_SPEC.md` 与公开的 WAV/Y4M 格式约定，
**没有为迁就实现而放宽任何断言**。生产代码未被修改。

复现方式：

```bash
# 1. 最小二进制证据 + 真实解码器观察（退出码 1 表示仍有未修复缺陷）
go run ./cmd/defectverify

# 2. 严格断言版证据测试（当前全部 FAIL，修复后应转 PASS）
go test -tags defectevidence ./internal/media/ -run TestDefect -v
```

证据文件位于 `defects/evidence/`，均为手工字节级构造的最小样例（46–826 字节），
由 `go run ./internal/media/defect_evidence_gen.go` 重新生成。

缺陷分类：

- **误接收（false accept）**：格式声明与实际样本/画面不一致，解码器却放行。
- **误拒绝（false reject）**：符合策略的标准素材被拒绝。
- **含义变更（meaning drift）**：素材被接收并进入分析/导出，但描述符或导出产物
  不再保持原始内容含义。
- 未发现可由外部输入触发的 panic/崩溃；资源放大受 `domain` 字节预算与帧数上限
  约束（见 `internal/media/pipeline_test.go` 的 `TestResourceBoundedness`）。

---

## WAV 缺陷（`internal/media/wav.go`）

### DEFECT-W1 — RIFF 尺寸声明与实际载荷不一致时被误接收

- 分类：误接收 / 声明-载荷不一致
- 证据：`defects/evidence/w1-riff-size-understatement.wav`（92 字节）
- 断言：`TestDefectW1RiffSizeUnderstatement`
- 现象：RIFF 头声明的尺寸只覆盖第一个 `data` 块，文件尾部又追加了一个完整的
  `data` 块。`InspectAudio` 的分块循环以 `len(data)` 而非声明的 RIFF 范围为界，
  声明之外的字节被当作正常块解析，隐藏的第二个 data 块（4 帧）成为实际样本，
  而描述符 digest 仍按整份字节计算，调用方无法发现声明范围被突破。
- 根因：`wav.go:41` 的循环条件 `offset < len(data)` 未与
  `riffSize+8` 取交集。
- 影响：可在“已声明大小”之外夹带样本数据，绕过基于声明尺寸的上游预算与审计。

### DEFECT-W2 — `fmt.byteRate` 与样本率/块对齐矛盾时被误接收

- 分类：误接收 / 声明-样本不一致
- 证据：`defects/evidence/w2-lying-byte-rate.wav`（76 字节，byteRate=1，
  实际应为 48000×2=96000）
- 断言：`TestDefectW2LyingByteRate`
- 现象：代码校验了 `blockAlign == channels*2`，却从未校验
  `byteRate == sampleRate*blockAlign`，矛盾头部直接放行。
- 根因：`wav.go:56-63` 读取 byteRate 后无任何交叉校验。
- 影响：依赖 byteRate 做时长/带宽估算的下游会得到与实际 PCM 不符的元数据。

### DEFECT-W3 — 重复 `fmt` / `data` 块被静默覆盖或丢弃

- 分类：误接收 / 声明-样本不一致
- 证据：
  - `w3a-duplicate-fmt.wav`（100 字节）：两个 fmt 块，第二个把单声道翻成立体声；
  - `w3b-duplicate-data.wav`（148 字节）：两个 data 块，第一个 32 帧被丢弃。
- 断言：`TestDefectW3DuplicateChunks`
- 现象：`format` 与 `pcm` 是普通变量，每次命中同名块就覆盖；没有“块唯一”检查。
  W3a 中实际样本按后一个立体声 fmt 解释（8 帧），W3b 中前一个 data 块的 32 帧
  无声消失，且均无错误。
- 根因：`wav.go:51-67` 的 switch 未记录块是否已出现。
- 影响：同一文件可产生多义解释；素材内容在解码时被静默改写。

### DEFECT-W4 — `data` 块出现在 `fmt` 块之前被误接收

- 分类：误接收 / 结构顺序违约
- 证据：`defects/evidence/w4-data-before-fmt.wav`（76 字节）
- 断言：`TestDefectW4DataBeforeFmt`
- 现象：循环先记录 `pcm`、后补到格式信息，块顺序约束完全未执行。标准 PCM WAV
  要求 fmt 先于 data；乱序文件应被拒绝或显式支持，当前是隐式放行。
- 根因：`wav.go:41-72` 对块顺序无状态约束。

### DEFECT-W5 — 不足 1 毫秒的有效素材描述符时长坍缩为 0

- 分类：含义变更 / 描述符错误
- 证据：`defects/evidence/w5-zero-duration-one-sample.wav`（46 字节，
  8 kHz 单样本）
- 断言：`TestDefectW5ZeroDurationDescriptor`
- 现象：文件被合法接收（1 帧、非空样本），但
  `DurationMS = frames*1000/sampleRate = 1*1000/8000 = 0`，与空音轨的描述符
  不可区分。整数截断没有最小粒度或四舍五入策略。
- 根因：`wav.go:136` 的整除公式。
- 影响：同步分析以毫秒为时间基准，零时长描述会污染事件时间与漂移计算的边界判断。

---

## Y4M 缺陷（`internal/media/y4m.go`）

### DEFECT-Y1 — 帧级 `FRAME It` 隔行参数被忽略（策略只允许逐行）

- 分类：误接收 / 声明-画面不一致
- 证据：`defects/evidence/y1-frame-interlace-ignored.y4m`（824 字节）
- 断言：`TestDefectY1FrameInterlaceIgnored`
- 现象：流头声明 `Ip`（逐行），首帧头携带 `It`（顶场隔行）。`InspectVideo`
  只检查 `fields[0] == "FRAME"`，帧上的 `I` 参数从不解析，隔行素材按逐行放行。
- 根因：`y4m.go:49-52`。
- 影响：违背 PROJECT_SPEC“only progressive Y4M is accepted”，隔行画面进入闪光
  检测后场序语义丢失。

### DEFECT-Y2 — 帧级 `FRAME C422` 色度声明被忽略，帧体仍按 C420 尺寸解释

- 分类：误接收 / 声明-画面不一致
- 证据：`defects/evidence/y2-frame-chroma-ignored.y4m`（826 字节）
- 断言：`TestDefectY2FrameChromaIgnored`
- 现象：帧头声明 C422（4:2:2，色度样本数不同），但帧体按 C420 的
  `w*h*3/2` 切分并接受，声明与实际平面布局直接矛盾。
- 根因：`y4m.go:49-57`，帧参数整体未参与校验。

### DEFECT-Y3 — 流头重复参数按“后者覆盖”静默处理

- 分类：误接收 / 声明-声明不一致
- 证据：`defects/evidence/y3-duplicate-width-token.y4m`（819 字节，
  同时含 `W16` 与 `W32`）
- 断言：`TestDefectY3DuplicateHeaderTokens`
- 现象：`parseY4MHeader` 对重复 `W/H/F/C` 不报错，最后一个 token 覆盖前者；
  最终按 W32 几何接收，第一个宽度声明被静默作废。
- 根因：`y4m.go:89-125` 缺少“每参数至多一次”的状态检查。

### DEFECT-Y4 — 导出路径把像素宽高比 `A16:9` 重写为 `A0:0`

- 分类：含义变更 / 导出内容失真
- 证据：`defects/evidence/y4-aspect-ratio-dropped.y4m`（432 字节）
- 断言：`TestDefectY4AspectRatioDroppedOnExport`
- 现象：`NormalizeY4M` 经 `EncodeY4M` 重新发射头部时硬编码 `A0:0`
  （`y4m.go:160`），解析阶段读到的 `aspect` 字段从未进入 `VideoInfo`，
  原始宽高比在 conform 导出的 `video-conformed.y4m` 中丢失。像素平面本身保持
  不变，但画面的显示语义被改变。
- 根因：`domain.VideoInfo` 不承载 aspect；`EncodeY4M` 写死默认值。

### DEFECT-Y5 — 标准 NTSC 分数帧率 `30000:1001` 被误拒绝

- 分类：误拒绝 / 合法素材
- 证据：`defects/evidence/y5-ntsc-fractional-fps-rejected.y4m`（437 字节，
  实际 29.97 fps）
- 断言：`TestDefectY5FractionalNTSCRejected`
- 现象：校验为 `fpsNum > 120` 即拒绝（`y4m.go:136`），比较的是原始分子而非
  有效帧率 `num/den`。广播行业标准的 30000:1001、24000:1001 等分子远超 120
  的合法分数帧率全部被挡在同步流程之外。
- 根因：帧率范围校验未做分数比较。
- 建议方向：对约分后的有效帧率（或 num/den 比值）施加 1–120 fps 边界。

---

## 模糊测试与资源失控结论

- `FuzzInspectAudio` / `FuzzInspectVideo` 各执行约 1,400 万 / 1,150 万次随机输入，
  未发现 panic、切片越界或无限循环；发现的唯一失败是测试预言机自身对重复 data
  块的取块偏差，已修正预言机（而非放宽断言），对应实现缺陷另由 W3 证据跟踪。
- 资源护栏：模糊输入按 `MaxAudioBytes=8MiB` / `MaxVideoBytes=24MiB` 跳过超预算
  样本；`TestResourceBoundedness` 用近 4 GiB 的 RIFF/块声明与 1920×1080 截断帧
  验证解码器按实际字节而非声明尺寸分配，并在 1200/1201 帧边界稳定拒绝。
- 修复本报告中的任一缺陷后，对应 `TestDefect*` 会从 FAIL 转 PASS，可直接提升为
  常规回归用例；`cmd/defectverify` 的退出码随之变化，无需改动任何现有断言。
