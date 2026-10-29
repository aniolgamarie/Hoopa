# T132 有限 fixture 编排准备验收

历史准备阶段记录。后续授权后的真实运行与修正见[隔离 Linux 实测](isolated-linux-lab.md)；本文原测试范围、旧 JSON 和未实测结论保留其当时范围。

日期：2026-10-24。延续“暂时没有，先完成原型准备”的授权，本轮实现[有限编排与完整材料契约](../../specs/001-hoopa-proxy-manager/contracts/fixture-run-v1.md)，连接既有 namespaced observer、固定 fixture 和离线 calibration。**本轮完成工具实现和合成验证，未运行真实 fixture 流量、未执行 BPF load/attach/TEST_RUN、未修改 cgroup。** transport 的真实子进程测试仅运行自有、无网络的本地 Python 输出/退出程序。

新机器证据归档为 fixture-run-validation.json（本地原件 `../../experiments/sourceidentity/fixture-run-validation.json`，不随Git上传），不得替换旧 calibration-validation.json（本地原件 `../../experiments/sourceidentity/calibration-validation.json`，不随Git上传） 或其他历史 JSON。T132 保持未勾选，139 项任务完成 21 项；原 [G01 0/100](g01-identity.md) 和[首次 active `RuntimeError`](source-recovery-closure.md) 继续保留，不新增正式身份分母，不放行 T137/T136 或 P0/P1。

## 交付行为

[run_fixture.py](../../experiments/sourceidentity/run_fixture.py) 默认在读输入或启动前拒绝，显式 `--allow-fixture-processes` 后才允许本轮有限编排。外部 launcher 必须在 attached 后将 driver 放入既有目标 cgroup，再执行命令。driver 自己只管理一个 server 和两个 client，不移动进程、不加载 BPF、不管理外部 PID。boot/PID/time namespace、time_for_children、cgroup v2 domain/leaf、inode/device/父目录与成员检查均为只读采样。

[fixture_processes.py](../../experiments/sourceidentity/fixture_processes.py) 保存自有 Popen，分别收集 stdout/stderr，保留退出观察和 EOF 的独立状态。每流最多 65536B，超限保留前缀并显式失败；spawn/输出/退出/取消/清理错误不转成成功。close 幂等，数据不清空，不因部分失败失去已有进程所有权。

driver 开始及首次 spawn 前核验 marker deadline 至少剩余 14 秒，驱动阶段预算 12 秒，清理轮询最多 2 秒/40 轮。每个client启动前后另需保留10秒驱动预算及12秒observer窗口，server返回与checker的启动事件也核对12秒余量；同步 OS 调用不受硬中断保证。开始采样、listening 观察、观察退出与完成时间均为用户态观测，**不是内核 exec、bind 或 exit 时间**。

[check_fixture_run.py](../../experiments/sourceidentity/check_fixture_run.py) 严格核对完整 11 文件目录、原始摘要/大小、argv、owned PID、退出/EOF/超限标志及时间顺序。固定文件名、安全只读打开、大小界限、重复 JSON 成员和非有限数拒绝均覆盖；既有 calibration 从 raw map 得到候选后才与 fixture token 对照。外部 scope evidence 绑定 registration、observer input、attached、整个 run receipt 和 observer result 的 SHA256，要求五项 operator 声明与 observer 实际零退出。报告必须写到 run 目录外。

一致材料只返回 `consistent_unverified`，外部声明为 `operator_reported_unverified`，所有 runtime、identity、association、completeness、scope、startup 能力仍为 `unverified`。点采样不能证明连续隔离或 attachment；摘要不能证明来源或外部真实性；run receipt 写入没有 fsync 或崩溃恢复保证。不得自动填造 scope 成功声明来让 checker 通过。

## 本轮验证记录

以下58项定向测试均通过（无skip），分组覆盖各自接口；没有重跑不受本轮影响的Go/BPF构建或旧完整矩阵。原始命令、退出码和稳定源码/日志SHA256见新机器证据。

| 验证组 | 范围 | 最终计数与结果 |
| --- | --- | --- |
| transport | args 数组/无 shell、最多三子进程、双流分段/EOF/退出顺序、非零/负退出、双流上限、部分 spawn、清理失败/幂等/快照；真实部分只运行无网络自有 Python 子进程 | 12项通过，exit 0；`fixture-processes-tests.log` |
| driver | fake Scope 与 children；boot/namespace/cgroup/成员/叶子/挂载根拒绝，FD 单 close，14 秒余量与 scope 采样耗时，完整 main 目录权限/摘要/源快照/错误保留，observer 停止阻断后续 spawn，真实 checker 接通合成材料 | 15项通过，exit 0；`spawn-reserve-corrected.log`（合计30项） |
| checker | 合成正例，摘要/文件集/大小/路径/符号链接/JSON、PID/argv/token、exit/EOF/超限、marker/namespace/时间顺序、五项 scope 声明和五个 SHA 绑定拒绝 | 15项通过，exit 0；同上合并日志 |
| observer marker | namespaced input 时间 namespace、marker 的 boot/monotonic/input hash 与既有 observer 行为回归 | 16项通过，exit 0；`marker-final.log` |

实际CLI另用合成文件验证四种结果：完整材料exit0且仅consistent_unverified；替换observer结果与缺少scope声明均exit1/rejected；缺执行开关exit2/refused。原始材料与报告在 `private/t132-run-preparation/synthetic-cli/`，合成fixture字节从未执行。

可复现定向测试命令如下；所有测试均不加载BPF，临时目录使用忽略的private：

```sh
TMPDIR="$PWD/private/t132-run-preparation/tmp" PYTHONDONTWRITEBYTECODE=1 \
  PYTHONPATH=experiments/sourceidentity python3 -m unittest -v test_fixture_processes
TMPDIR="$PWD/private/t132-run-preparation/tmp" PYTHONDONTWRITEBYTECODE=1 \
  PYTHONPATH=experiments/sourceidentity python3 -m unittest -v test_run_fixture test_check_fixture_run
TMPDIR="$PWD/private/t132-run-preparation/tmp" PYTHONDONTWRITEBYTECODE=1 \
  PYTHONPATH=experiments/sourceidentity python3 -m unittest -v test_run_observer
```

开发与定向日志保存在忽略目录 `private/t132-run-preparation/`。本轮已使用的分组日志名包括 `fixture-processes-tests.log`、`fixture-driver-first.log`、`driver-integration.log`、`check-fixture-run-tests.log`；最终归档的准确日志路径和摘要以新机器 JSON 为准。合成 main 使用最小 ELF 字节作为固定 fixture 材料，fake children 提供完整 JSONL，未执行该二进制；接通 checker 不能当作真实 socket/BPF 校准。

## 状态与未验证范围

| 状态 | 本轮边界 |
| --- | --- |
| 已完成 | 有限进程收集、固定启动顺序、取消/收尾、完整材料目录与外部声明绑定的实现及定向准备验证；共58项定向测试通过 |
| 进行中 | T132 的真实小样本校准、原 accepted socket 唯一关联、名称/启动身份、在线与核心接线 |
| 失败待决策 | 本轮无未决代码失败；首spawn/晚listening/慢spawn预算与结果绑定已补齐。tests-first及新增守卫改变旧测试触发顺序的失败日志保留（样本由18秒改17秒，定向核验14秒分支）；原G01 0/100与首次active RuntimeError不改判 |
| 环境不足未验证 | 真实 fixture 双 cookie、helper/verifier/load/attach/stop、完整 scope/attachment 连续性、初始 PID namespace 与外部实际放置；不记作通过，也不阻塞无关文档工作 |

未来操作命令、外部声明的准确字段和限额见[契约](../../specs/001-hoopa-proxy-manager/contracts/fixture-run-v1.md#5-未来-lab-命令与留证顺序)。执行者必须先取得相应隔离 lab 授权并独立保留原始范围/时序依据；先完成两个 client 的有限正反例，再判断是否有条件继续在线方案和核心兼容工作。旧制品、正式门槛、最低平台声明和性能预算均不因本轮准备而改变。
