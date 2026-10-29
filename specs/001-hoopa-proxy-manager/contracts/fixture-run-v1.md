# T132 有限 fixture 编排与材料核对契约 v1

状态：为未来隔离 Linux/amd64 lab 准备的实验契约。接续[小规模校准契约](connector-calibration-v1.md)，只补充有限进程编排、输出收集与离线材料绑定。`completed_unverified` 和 `consistent_unverified` 均不认证运行身份、采集完整性或隔离范围。T132 未完成，139 项任务完成 21 项；原 G01 **0/100** 与首次 active `RuntimeError` 保留。

实现为 [run_fixture.py](../../../experiments/sourceidentity/run_fixture.py)、[fixture_processes.py](../../../experiments/sourceidentity/fixture_processes.py)、[check_fixture_run.py](../../../experiments/sourceidentity/check_fixture_run.py) 和 [run_observer.py](../../../experiments/sourceidentity/run_observer.py)。本轮实际验证范围见[验收说明](../../../docs/validation/fixture-run.md)。

## 1. 启动前提与权限边界

driver 默认拒绝运行，必须显式提供 `--allow-fixture-processes`；未提供时在读取输入、创建目录或启动子进程前返回 `status="refused"`、exit 2。显式开关仅授权该次实验进程，不代表身份能力通过。

driver 不创建、移动或修改 cgroup，不加载 BPF，不接收外部 PID，不管理已有进程。外部 lab 必须先预登记并启动 namespaced observer，等到 `attached_observer` 后，才把 driver 放入已经存在的目标 cgroup，再执行 driver。该外部放置必须在任何 fixture socket 创建之前完成。driver 自己启动固定一个 server 和两个独立 client，由子进程继承该放置；不能从普通 shell 启动 fixture 后再移动它们。

启动时 driver 核验：

- Linux/amd64；固定 SHA256 的 fixture 是 ELF64 little-endian、EM_X86_64 的 ET_EXEC 或 ET_DYN，私有复制后再次核对摘要。
- registration schema 为 `sourceidentity-calibration-registration-v1`，恰有两个不同的 32 位小写 hex token；generation/cgroup/netns/port 与 namespaced observer 的 config 相同。
- observer input 的 cgroup inode 与 registration cgroup 相同；input 与 attached 的 generation/boot/duration 和原始 input SHA256 一致。duration 是非 bool 有限数字，`0 < duration_seconds <= 60`。
- driver 与 observer 的 `boot_id`、PID namespace inode、time namespace inode 相同；driver 当前 `time_for_children` 与当前 time namespace 一致。相同 namespace inode 不能证明处于初始 PID namespace，仍需外部声明与证据。
- 目标是 cgroup v2 的 `domain` leaf，inode/device 与 observer input 一致；父目录仍属 cgroup v2 且 inode 不等于目标，拒绝挂载根。初始成员样本必须只有 driver，后续样本仅允许 driver 和本对象启动的孩子，无子 cgroup。
- observer 的 `result.json` 尚未出现；后续检查中一旦出现即停止推进，不能继续启动后面的 client。

这些检查是点采样，不能证明两次检查之间的连续隔离、附着连续性、无外部 socket 或无同 UID 干预。已有 socket、预登记先于流量、初始 PID namespace 和 hook 连续性仍由 lab 独立留证。

## 2. 进程顺序、时钟与收尾

`deadline_monotonic_ns = int(attached.observed_monotonic_ns) + int(duration_seconds * 1000000000)`。driver 开始时及首次 server spawn 前均要求至少剩余 **14 秒**。驱动阶段自进入 `drive` 起最多按 **12 秒**预算推进，且受 observer deadline 限制；清理轮询另受最多 **2 秒、40 轮**限制。

server 的 Popen 返回后还须保留至少12秒observer窗口。每个client启动前后都要求驱动阶段至少剩余10秒、observer窗口至少剩余12秒；延迟listening或缓慢spawn不能据旧时间继续启动后续进程。checker也核对每个 `*_started` 事件距observer deadline至少12秒。同步调用仍不能被该预算硬中断。

driver 先启动 server，观察到它 stdout 第一条完整 `listening` JSONL 后，再按 registration token 顺序启动 client1、client2。启动参数只能是数组，`shell=False`，stdin 为 DEVNULL，stdout/stderr 为 PIPE，不建立额外进程组：

```text
[<ABS_RUN_DIRECTORY>/fixture, --role, serve, --generation, G, --port, P, --count, 2, --allow-loopback]
[<ABS_RUN_DIRECTORY>/fixture, --role, client, --generation, G, --port, P, --token, TOKEN_0, --allow-loopback]
[<ABS_RUN_DIRECTORY>/fixture, --role, client, --generation, G, --port, P, --token, TOKEN_1, --allow-loopback]
```

三个 label 固定为 `server`、`client1`、`client2`；每个 label 仅登记一次。后端持有原始 Popen，再设置管道；部分 spawn 或管道设置失败仍保留先前所有权供清理。它仅通过这些 Popen poll/reap/kill，不根据外部数值 PID 发信号。

单线程 selector 每次读不超过 4096B，每次 pump 最多处理 16 个就绪项，timeout 不超过 0.1 秒。每个孩子的 stdout、stderr 分别最多 65536B，超限保留前缀、设置 `truncated=true` 并报错，不能静默截断当作成功。进程退出与双流 EOF 分别判断；只有三个孩子 exit 0、双流 EOF 为 true、truncated 为 false，且输出校验成功，才可能写 `completed_unverified`。

收尾先 poll 自有 Popen，仅对确认仍运行的自有孩子 kill，再有界 drain/poll。poll 失败时不凭 PID 猜测 kill；无法确认退出或 EOF、超限、kill/close/selector 错误都保留在错误列表，不覆盖主错误。close 幂等，句柄最终单次关闭，快照与已有 stdout/stderr 不清空。SIGINT/SIGTERM handler 只设置取消标志，在安全边界推进清理。

**预算不能硬中断 OS 调用。** Popen、kill、poll、read、close、文件读取和作用域采样本身可能同步阻塞；12+2 秒不是整个命令的硬实时保证。receipt 的 `started_monotonic_ns` 是启动调用前采样，`observed_exit_monotonic_ns` 是 poll 观察到退出时采样，事件时间是用户态观察时间，均不等于内核 exec、bind 或 exit 时间。

## 3. 完整目录与 receipt

成功材料目录恰有以下 **11 个普通文件**，不允许额外文件、子目录或符号链接。输出父目录必须已存在且为绝对路径；driver 新建私有目录模式 0700，fixture 为 0700，其余文件为 0600。

```text
run.json
registration.json
observer-input.json
attached.json
fixture
server.stdout.jsonl
server.stderr.log
client1.stdout.jsonl
client1.stderr.log
client2.stdout.jsonl
client2.stderr.log
```

`run.json` 的字段如下；uint64 坐标与时间使用正 uint64 的规范十进制字符串，无前导零。

| 字段 | 约束 |
| --- | --- |
| `schema` | `sourceidentity-fixture-run-v1` |
| `status`、`errors` | 完整核对只接受 `completed_unverified` 且 `errors=[]`；运行失败保留 `failed` 和阶段错误 |
| `generation`、`boot_id` | 与本轮 registration/input/attached 相同 |
| `controller_pid` | 正 JSON 整数，不等于三个孩子 PID |
| `pidns_inode`、`timens_inode` | 与 input 的 `observer_pidns_inode`、`observer_timens_inode` 相同 |
| `started_monotonic_ns`、`finished_monotonic_ns`、`deadline_monotonic_ns` | 开始不早于 marker；结束严格早于 deadline |
| `identity_capability`、`association`、`completeness`、`scope_isolation` | 均为 `unverified` |
| `artifacts` | 恰有 `registration`、`observer_input`、`attached`、`fixture`，对应上述固定文件名 |
| `children` | 恰有 server/client1/client2 |
| `events` | 下述六事件，按固定顺序记录 |

每项 artifact 为 `{name,sha256,bytes}`，摘要针对原始字节；stdout/stderr 也各用此结构。单项子进程字段为 `label`、`argv` 数组、`pid`、`started_monotonic_ns`、`observed_exit_monotonic_ns`、`exit_code`、`stdout`、`stderr`、`stdout_eof`、`stderr_eof`、`truncated`。失败材料可能缺项或含 null，checker 必须拒绝，不能自动补成成功。

事件恰为 `preflight_complete`、`server_started`、`listening_observed`、`client1_started`、`client2_started`、`children_completed`；每项为 `{event,observed_monotonic_ns}`。时间非降，可相等；所有事件处于 driver 开始与结束之间。spawn 采样不晚于对应 started 事件；两个 client 均在 listening 观察之后开始，client2 不早于 client1_started；children_completed 不早于全部观察退出。server spawn 也需满足 14 秒余量。

server stdout 恰为 listening 加两个 serve connection；每个 client stdout 恰有一条 connection，其 PID 等于对应自有子进程 PID，token 按登记顺序对应。三个孩子 PID 互异。stderr 原样保留，空或非空本身不代替退出码判断。

文件写入采用排他创建，**receipt 没有 fsync 或崩溃恢复保证**。写入中断可能留下不完整目录；原始摘要绑定材料内容，不认证内容的外部真实性或操作者来源，也不抵御同 UID 主动篡改。

## 4. 外部 scope 声明与离线判定

checker CLI 为：

```sh
python3 -B experiments/sourceidentity/check_fixture_run.py \
  --run-directory "$RUN_DIRECTORY" \
  --observer-result "$OBSERVER_RESULT" \
  --scope-evidence "$SCOPE_EVIDENCE" > "$CHECK_REPORT"
```

`RUN_DIRECTORY` 必须是绝对路径。observer result、scope evidence 和输出报告必须放在 run 目录外；重定向报告进 run 目录会使固定 11 文件核验失败。checker 不执行 fixture，也不访问 socket/BPF/cgroup。

原样导出的跨 VM 材料可以另加 `--original-run-directory "$ORIGINAL_RUN_DIRECTORY"`；Python 接口对应 `check_run(..., original_run_directory=...)` 可选 keyword，默认为 `None`。未指定时保持原行为：三个孩子的 `argv[0]` 必须等于当前宿主 `RUN_DIRECTORY` 的绝对路径加 `/fixture`。指定时，仅把这三项的期望值改为声明的原目录字符串加 `/fixture`，其余 argv 和所有摘要绑定仍逐项核对。原目录必须是规范 POSIX 绝对目录字符串，拒绝 NUL、相对路径、`.`/`..` 路径段、重复 `/`、尾部 `/` 及根目录 `/`；checker 不修正规范化、不解析或打开该 guest 路径。宿主材料始终从当前 `RUN_DIRECTORY` 的目录描述符按固定 basename 读取，保留 `O_NOFOLLOW` 等校验。

迁移时必须保持原始 receipt、observer result、scope evidence 和材料字节，不得为匹配导出路径重写或重新生成 receipt，也不得重新绑定原始 scope 摘要。显式选项只声明原 argv 路径，不认证 guest、VM、操作者或材料来源。报告增加 `original_run_directory` 和 `original_run_directory_claim="operator_declared_unverified"`，所有能力字段及 scope 声明语义保持 `unverified`；未指定该选项时不增加这两项。

metadata 与外部 JSON 单文件上限 4MiB，fixture 上限 64MiB，stdout/stderr 各 65536B；输入只接受普通文件，固定 basename，拒绝符号链接、路径穿越、额外/缺失文件、摘要/大小矛盾。全部 JSON/JSONL 使用严格解析，拒绝重复成员和非有限数字。

外部 scope evidence 是操作者独立准备的 JSON，不能由 driver 或 checker 自动生成“通过声明”：

| 字段 | 值或约束 |
| --- | --- |
| `schema` | `sourceidentity-lab-scope-v1` |
| `evidence_kind` | `operator_attestation` |
| `generation`、`boot_id` | 与 run 相同 |
| `registration_sha256` | 原始 registration.json 的 SHA256 |
| `observer_input_sha256` | 原始 observer-input.json 的 SHA256 |
| `attached_sha256` | 原始 attached.json 的 SHA256 |
| `fixture_run_sha256` | 整个原始 run.json 的 SHA256 |
| `observer_result_sha256` | 外部本轮 observer result 原始字节的 SHA256 |
| `observer_exit_code` | JSON 整数 0，不能用 bool |
| `claims` | 恰有下述五项 |

五项 claim 的键为 `placement_before_socket`、`attachment_continuity`、`no_preexisting_sockets`、`initial_pid_namespace`、`preregistration_before_traffic`。每项恰为 `{status:"operator_reported",detail:非空字符串}`，detail 去除首尾空白后非空且原长度不超过 4096。操作者应根据实际独立记录逐项说明，不能把模板默认值或合成测试声明当成真实证据。缺项、绑定摘要错误或 observer 非零退出都拒绝。

checker 按 server/client1/client2 顺序读取原始 JSONL，调用既有 `calibration.check_bundle`，从 raw map 独立构造候选后再核对 token/原 socket witness，不用 oracle 重写候选。成功为 `status="consistent_unverified"`、exit 0；不一致为 `rejected`、exit 1。报告保留 run、observer result、scope evidence 的输入路径与原始 SHA256，`external_claims="operator_reported_unverified"`。identity_capability/runtime/association/completeness/scope_isolation/startup_identity/preregistration 均保持 `unverified`。exit 0 只表示材料有限一致，不证明范围声明、连续性或正式身份能力。

## 5. 未来 lab 命令与留证顺序

以下命令仅供已获授权的未来隔离 lab；本轮未执行。先遵守[校准契约的环境与预登记顺序](connector-calibration-v1.md#4-未来隔离-lab-的执行顺序)，固定 namespaced 对象、libbpf、fixture 的摘要，以及两个 token、generation、netns cookie、目标 cgroup 和端口。不得直接重跑正式 100 流。

外部操作者在独立控制会话启动 observer，保存其实际退出码、输出目录、原始日志与附着/范围记录；观察窗口必须足够完成后续启动，最长 60 秒：

```sh
python3 -B experiments/sourceidentity/run_observer.py \
  --allow-bpf-load --profile namespaced \
  --object "$OBJECT" --object-sha256 "$OBJECT_SHA256" \
  --libbpf "$LIBBPF" --libbpf-sha256 "$LIBBPF_SHA256" \
  --cgroup "$CGROUP" --generation "$GENERATION" \
  --netns-cookie "$NETNS_COOKIE" --destination-port "$PORT" \
  --duration 60 --output-parent "$OBSERVER_OUTPUT_PARENT"
```

等待该轮附着 marker 后，外部 launcher 先完成 driver 的目标 cgroup 放置，再在这个已放置的执行上下文运行以下命令。仓库不提供或猜测 launcher 的放置命令；变量须来自同轮固定材料，OBSERVER_DIRECTORY 是实际 observer 输出目录：

```sh
python3 -B experiments/sourceidentity/run_fixture.py \
  --allow-fixture-processes \
  --registration "$REGISTRATION" --observer-directory "$OBSERVER_DIRECTORY" \
  --fixture "$FIXTURE" --fixture-sha256 "$FIXTURE_SHA256" \
  --output-parent "$FIXTURE_OUTPUT_PARENT"
```

保存 driver 输出的目录、退出码和错误；等待 observer 停止并保留实际退出码与 result。由操作者根据独立记录准备 scope evidence，再在全部材料目录外运行 checker 并保存报告与退出码。任一进程失败、范围记录不足或材料核验拒绝都保留原始材料，不改成能力通过。真实 helper/内核兼容性、双 cookie、完整 scope/stop 与启动身份仍需单独验收。
