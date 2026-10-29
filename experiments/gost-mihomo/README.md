# P0 组合实验 harness

> 运行报告与准备/诊断过程材料保留在本地原位置，不随 Git 同步。“本地原件”代码路径不是源码 clone 文件；正式 acceptance、构建身份与源码保留。只读验收 gate 仍需完整且 hash 匹配的本地报告/私有原件，不能仅凭本页结论宣称重新验收通过。

这是可执行的隔离校准工具，不是 Hoopa 产品 CLI。身份/流量/recorder 单元测试使用合成观测、内存连接和配置渲染；沙箱端口隔离测试需要绑定 loopback socket。默认测试不启动外部核心、不访问外网。真实组合运行由显式命令或集成开关触发，权限不足时记录未验证，不自动提权。

## 构建与单元验证

在仓库根目录使用已核对的 Go 1.27.3；以下路径是本次准备的临时工具链，不代表系统已安装 Go：

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -run 'Test(Identity|Flow)' -v ./experiments/gost-mihomo
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go vet ./experiments/gost-mihomo
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go build -o /tmp/hoopa-p0 ./experiments/gost-mihomo
```

只用标准库和仓库内标量类型，不下载模块；不修改 GOSUMDB。`TestFlowIntegrationOptIn` 默认明确 skip/unverified，不能计作核心或 P0 验收通过。

## 显式校准

下面命令会创建私有沙箱、启动两核、绑定 loopback 并产生受控流量，仅在实验权限已具备时执行：

```sh
/tmp/hoopa-p0 \
  --mihomo /data/1/weixiaoxian.wxx/dev_tool/clash_env/mihomo \
  --mihomo-sha256 78f3d225a53f8345db5b1f3f147d155ef5a1d56843931f6d61f908045cefb789 \
  --gost /home/weixiaoxian.wxx/.local/bin/gost \
  --gost-sha256 04ee3969e59fe86924616002464145be47caf4a89f0501efdfb6458f206ddc4f \
  --base /tmp --timeout 45s
```

`--mihomo`/`--gost` 必须绝对路径并匹配预期 SHA256；分别调用 `-v`/`-V`，仅提取版本字段。当前候选版本是 Mihomo 1.19.26、GOST 3.3.1；未核对官方资产来源时 manifest 的 source_verified 始终 false。`--agent "$AGENT_PATH"` 可记录已明确选择的 Agent 的 SHA256 和 `--version` 字段，**不会执行真实 Agent 工作流或发送付费请求**。真实应用正常/持续工作流由后续显式探针验收，curl 只能校准。

默认配置只有受控 IP/direct 校准；没有 provider/地区选组验收，不修改任何现有代理。可额外指定 `--upstream-proxy 127.0.0.1:10808`，显式借用已有本地 SOCKS5 节点用于之后的外网 Agent 测试；本 harness 的受控 loopback 目标仍明确 DIRECT，不访问外部业务端点。

公开的 [mihomo.yaml](mihomo.yaml) 与 [gost.yaml](gost.yaml) 是嵌入模板，无可用凭据。实际配置在新 0700 沙箱内以 0600 写入；各 proxy 入口、controller 和 metrics 使用独立随机凭据。Mihomo 设置 authentication、空 skip-auth-prefixes，controller secret 独立；GOST 的 handler.auth、connector.auth、metrics.auth 分别配置，默认没有 GOST WebAPI。字段依据：[GOST 认证](https://gost.run/en/concepts/auth/)、[GOST metrics](https://gost.run/en/tutorials/metrics/)、[Mihomo 通用配置](https://wiki.metacubex.one/en/config/general/)。本次固定二进制已通过配置与9项认证子断言，详见[真实运行记录](../../docs/validation/p0-workflow.md)；其他版本须重新核验。

Mihomo 先以新私有 `-d` 目录执行 `-t`，再启动；配置不含 Geo/规则资产下载、DNS/TUN/sniffer 或自动 GEO 更新。`find-process-mode: always` 仅为收集活动 metadata；GOST 转发后的 PID 不能冒充原应用 PID。子进程使用显式参数数组、独立 HOME/XDG 和白名单环境，丢弃继承的代理变量，加入本 run 的 HOOPA_EXPERIMENT_RUN_ID。

## 保存的事实

正常 stdout 只有一个脱敏 JSON（`--help` 除外），字段包括 run_id、directory、reason_code、kept_cores、flows、security、counters、gates。flows 逐项记录 Mihomo 对照、GOST HTTP 与 SOCKS5 路径的固定下载/上传、HTTPS CONNECT、长连接、503、超时和原始 TCP 收发结果；client_round_trip_ms 仅为客户端往返，不是 forward_setup 或业务首字节。

受控 payload：上传 16 KiB、下载 32 KiB、长连接 128 KiB。原始 TCP 服务先核对上传内容再返回固定下载内容。请求始终显式使用代理，即使目标是 localhost 或父环境 NO_PROXY=* 也不绕过代理。长连接首段读取后保存活动连接和 metrics 中间快照；快照能否捕获连接是实际观测，不能据此推导短连接完整历史。

私有目录保留 manifest.json、events.jsonl、两份实际 YAML、核心日志、metrics-before.txt、各路径 metrics/连接中间快照、metrics-after.txt、connections-before/after.json 与 result.json。所有文件均私有；核心原始日志、metrics 标签和连接 metadata 可能含敏感目标，只留私有目录，不直接提交 Git。只读原始快照请求使用独立认证，不调用改选择或 delay 端点。

security 保存 controller、metrics、Mihomo HTTP、GOST HTTP 的授权/未授权结果以及 GOST SOCKS5 认证拒绝/成功结果，不用单一 running 隐藏失败。counters 仅对同 epoch、同完整标签集合逐 series 做差后求和；标签顺序归一、重复 series 拒绝、下降/消失/新 series 明确不可用，未观测方向保留 null。Prometheus 原值以精确有理数解析，必须是非负整数且小于 2^53；大于等于该界限不能称逐字节精确。差量数学验证通过也不表示已解释 CONNECT/TLS/协议开销，G02 仍未验证。

G01 的纯 oracle 要求运行前真实登记、至少 100 条可识别短流、逐条原 socket/PID/name/启动身份/boot/目标及终末字节；缺观察不通过，PID 复用和转发器冒用失败，共享 socket 只能按预定原因未知。当前没有原始源采集或跨跳身份适配，因此实际 G01、G03 不通过此 harness 自动关闭。G04–G07 的生命周期/规则恢复/SQL/规模门槛也尚未执行。所有真实子断言和门槛状态分开保存；完整门槛见[原型验收](../../docs/prototype.md)。

## 保留核心供后续 Agent 探针

显式增加 `--keep-cores` 时，只有认证检查成立并取得每核 Linux 启动身份后才保留核心。私有 endpoints.private.json 包含带认证的代理 URL、controller/metrics 凭据，以及每核 PID、OS start identity、boot identity、binary SHA256、工作目录、配置路径和 run ID。不要把这个文件或 URL 放进公开报告。

默认实验结束仅通过本次启动所得 cmd.Process 句柄清理所属核心，不按名称 kill。保留模式下受控 HTTP/TLS/TCP 服务器随 harness 退出而结束，核心仍使用已写私有配置；后续 Agent 应使用显式选择的测试目标。这不证明 SSH/注销/管理器崩溃生存或生产接管，G04 仍为 unverified。

后续清理必须先复核 `/proc/PID/stat` 的 starttime、boot identity、`/proc/PID/exe` 摘要、工作目录/配置和 HOOPA_EXPERIMENT_RUN_ID 与记录一致，再显式停止；不能仅拿 PID 文件盲杀。无法核验时报告冲突，由主代理处理。沙箱证据默认保留供分析。

如需通过测试入口显式执行组合实验，先设置 HOOPA_P0_INTEGRATION=1 及 HOOPA_TEST_MIHOMO、HOOPA_TEST_MIHOMO_SHA256、HOOPA_TEST_GOST、HOOPA_TEST_GOST_SHA256，然后运行 `go test -run '^TestFlowIntegrationOptIn$'`（沿用上面的固定 Go/env）。缺任何输入仍 skip/unverified。当前实现不需要或请求特权；运行环境禁止 socket 时交主代理安排经授权的实际验证，不自行改变权限。

## T017 原生 recorder 投递探针

在前述固定核心命令追加 `--recorder-probe`，即可仅为本次实验启用 GOST 已有的 HTTP recorder plugin。追加 `--recorder-reject-first` 会对首个合法 JSON 投递回复 HTTP 503 与 `ok:false`，后续合法投递回复 200 与 `ok:true`；后者必须同时启用前者。两入口都挂接 `recorder.service.handler`，不启用 body 记录、解密或新身份适配。配置和协议依据 [GOST recorder 官方文档](https://v3.gost.run/en/concepts/recorder/)；固定版是否接受配置仍须实际核心验证。

collector 在配置生成前绑定随机 loopback 端口，随机 token 路径仅写私有配置。只接收 POST：每次最多 1 MiB，最多 256 次认证路径请求，header/read/write 均有超时。outer 的 base64 data 和解码记录分别保存为 0600 的 observer-NNNN-outer.json、observer-NNNN-decoded.json；其中可能包含 Proxy-Authorization、域名和原始错误，禁止公开这些原始文件。不得从未启用 body 记录推断 header 无秘密。

脱敏 result 的 observer 和私有 observer-summary.json 只列尝试/接收/拒绝/解码/无效数量、重复 sid 数量、固定字段的存在数量与 JSON 类型，以及 inputBytes/outputBytes 均为精确非负 uint64 整数的记录数。JSON 使用 UseNumber，不经 float64；整数即使超过 2^53 也保留原字面值，字符串、小数、负数或 uint64 溢出不计作精确字节。固定字段清单中缺失的身份、序号、epoch、时间字段仅报告缺失，不补造。字段有值不证明其归属、终末完整性或计时口径。

校准结束后保留最多一秒投递窗口，再关闭 collector 并保存摘要。保留核心模式也关闭 collector，后续 Agent 的 recorder 投递会失败；本探针只检验校准期间的原生投递行为。重复 sid 可用于观察拒绝后的实际行为，但不等于重试保证、可靠序号、持久重放或恰好一次；没有投递则明确未验证。所有 G 门槛仍为 unverified，原始事件是否含所需字段及失败投递边界交主代理裁决。metrics 的 run ID epoch 只用于同一存活 GOST 进程的前后采样，不覆盖核心重启，G04 仍未验证。

保留核心前再次核验 done 状态、starttime、boot identity、当前 exe 摘要、cwd、完整 cmdline 与 run nonce；失败则不标为 kept，并仅清理本次拥有的进程句柄。这仍不是生产接管或原子生存承诺。

定向单测（不绑定 socket、不启动核心）：沿用前述固定 Go/env，运行 `go test -run 'Test(Observer|Identity|Flow)' -v ./experiments/gost-mihomo`；`go vet ./experiments/gost-mihomo`，构建目标改为 `go build -o /tmp/hoopa-p0-observer ./experiments/gost-mihomo`。单测通过不代表固定核心 recorder 实测通过。

## 已送达事件的实验 journal 接线

Linux 上在固定核心命令追加 `--recorder-probe --recorder-journal`，启用 [实验 journal](../telemetry/README.md)。`--recorder-journal` 必须与 probe 同用，默认不启用；其他平台明确 unsupported。收集器创建独立 0700 的 recorder-journal 子目录，内部锁/数据文件为 0600，沿用已验证的完整帧、Sync、poison 与拒绝损坏尾部语义。

GOST 启动并取得 OS starttime/boot identity 后、任何数据入口请求之前，才绑定本世代的 source epoch：将固定结构 role、boot、pid、start、binary_sha256、start_nonce 序列化为 JSON 后计算 SHA256，加 `gost:` 前缀。start_nonce 取自核心启动时保留的 CoreInstance.RunID，不使用当前 manager/collector run 作为 hash 输入。原 boot/core 身份、随机 recorder 路径与精确两个服务名写入 0600 的 observer-journal-source.private.json。世代未绑定、service 不在白名单、SID 不是合法 ParseID token 时返回 503；不生成替代 SID。这些限定只表示该随机 URL 收到的、声称属于本次固定 GOST 世代的服务事件，不证明原应用身份，也不提供对同用户恶意伪造的来源认证。

journal 以原生 SID 为事件 ID、decoded 原始字节为 payload：完整 Write + Sync 成功才回复 200。同 SID 同字节重投返回原 seq，不新增记录；同 SID 异字节冲突或结果不确定均返回 503，并 poison 后续回调。原始快照保留原策略，但快照成功本身不构成 journal 确认。超过 journal 64 KiB payload 上限也拒绝，不能因 HTTP 包体上限较大而绕过。

与 `--recorder-reject-first` 同用时，首个成功持久化的事件随后被回复 503，模拟 ACK 丢失；重投相同原文才去重。它不模拟落盘前丢失。无 journal 时仍按原探针方式拒绝首个合法投递。原生 GOST 是否实际重投、同 SID 是否跨多条不同事件使用，须固定二进制实测；冲突应交主代理重规划，不能自动改造 ID 或归并内容。

finish 最多保留一秒投递窗口，关闭 HTTP 后持 collector mutex 置 closed，等待中的 handler 返回 503；此后才关闭 journal。再重开同目录，完整扫描与 Sync 后按 seq/epoch/ID/原文/数量核对此前确认记录，保存 0600 的 observer-journal-summary.json。stdout 的 observer.journal 只包含状态、固定原因和 unique/replayed/deduplicated/dedup_conflict 等计数，不含 SID、epoch 或私有 payload。重开失败、内容不一致或关闭失败均明确失败，不截尾。summary 中 passed 仅指本次已送达记录的持久化重开核验成功，所有 G01–G07 仍为 unverified。

该接线不修复 GOST 未投递，不声称完整源事实、最终字节或生产 SQL 已实现，也不对原始流量另做计量加总。保留核心模式下 collector 仍会关闭，后续 Agent 投递不会由该 journal 接收。

沿用固定 Go/env，定向单测运行 `go test -run 'Test(Observer|Identity|Flow)' -v ./experiments/gost-mihomo`，然后 vet；构建目标为 `go build -o /tmp/hoopa-p0-journal ./experiments/gost-mihomo`。单测使用私有临时文件与 httptest.NewRequest，不绑定 socket，不启动核心或 Agent；实际固定核心运行由主代理执行。
