# T026 固定基础核心与离线启动组合验收

2026-10-27。root 接受以下有限组件及组合证据，T026 完成；P1 尚未交付。
此文承接[核心候选](p1-core-control-candidate.md)及[控制适配器](p1-core-controller.md)
历史阶段记录；原失败、私有 receipt 和旧制品不覆盖。

## 已批准能力

[基础能力批准记录](../../experiments/mihomo-events/approved-basic-core-v1.json)固定
Linux/amd64、basic/control-v1、核心 SHA-256
`2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`，内置版本 `1.10.0`。
源码仍为固定上游 v1.19.26 加已记录补丁，打印版本不代表上游源码版本。
`ApprovedBasicCapability(path)` 将本地安装位置绑定到这份批准记录，纯计算、不下载
或执行资产；实际 Start 与接管仍核验已打开的二进制 FD、完整进程身份和真实 controller。
这些声明由内部可信代码产生，不接受核心自报能力替代证据。

批准范围只有认证 activate、closed-start 和完整不可变 snapshot。G04 新入口门控、
持久出生/登记及同实例核验，G05 完整基础组/规则顺序，G07 最小认证控制端点取得
以下有限证据。G01–G03 不从旧 hash 转移；basic 不启用来源身份或计量，完整身份模式
在 T138/T077 另验，进程规则缺少能力必须拒绝。P1 生命周期使用独立 basic fixture，
旧完整身份 fixture 保持 SHA-256 `840fb81c…138a9f` 及全部原规则。

## 离线启动与实际组合

管理器以当前自身映像执行专用 launcher，独立 session、私有日志及空 stdin，不继承
父 shell 的代理环境。出生目录 FD 只授予该 launcher；launcher 持久记录真实 PID/启动
身份后关闭它，再通过已核验的同一静态 ELF FD 执行核心，PID 保持不变。固定 argv、
私有配置和工作目录均核验；不支持动态 ELF/script，错误不回退到其他二进制。
父管理器保留实际 Child.Wait；取消等待或关闭管理句柄不会发送信号。

限定 launcher race 最终 8 顶层/33 子节点，40 pass、1 默认实际核心 opt-in skip；
其中 3 个空 dispatcher 单列，37 个通过节点含断言。首轮确实发现 LaunchChild 整值
复制与 waitErr 发布竞争；修复为不变句柄指向独立退出状态，在 done 关闭后读结果，
保留触发该竞争的值/指针格式化断言，最终 race 通过。首次失败原件未改标。

另一次显式真实核心组合为 **1 核心、1 条 HTTP CONNECT echo 流**，子 PID 1726718：

1. 固定资产预检 → PrepareLaunch → 出生 → 同 PID exec；原 pidfd、argv、nonce 与
   同 TCP 连接 owner 证明通过，核心不再持有出生目录 FD。
2. 两个应用入口 closed，完整快照核验后持久登记，随后认证 Activate 并完成真实回显。
3. 关闭管理句柄和 Store 后，核心与旧流继续；重开 Store 核验同一出生实例并登记 drain。
4. Drain 后旧流继续，新连接拒绝；原 activate 重投返回历史 opened，当前仍 draining。
5. 原 pidfd TERM，直接子进程 Wait 退出 0。

这次使用测试能力 fixture；后续批准记录绑定其有限证据，不谎称当时已经使用生产批准。
未杀死管理器、未验证全量恢复/自动接管；临时运行文件未逐个归档，证据为冻结测试源、
原始断言输出及独审。T033 固定 CLI 内部分派仍待接线，Start 成功本身不等于 ready。

## 独立完整基础输入

[basic-verified-bundle.json](../../testdata/runtime/basic-verified-bundle.json)新增两份
完整基础配置：app-first 和 domain-first，分别保留全部四条规则。各自先制作独立预期
快照，启动实际固定核心后完整比较，认证开放，再经本地受控上游验证 HTTP CONNECT
与 SOCKS5 的真实选路。两个核心 PID 1717171/1720294，合计四条业务流；每次 drain
后旧流继续、新连接拒绝，正常退出 0。两份配置分别选 A/A、B/B，证明实际顺序差异。

这是直接核心校准，不含产品 Operation；脚本在运行后封存，没有预登记逐命令源码
hash，也没有逐 API 原始包。两个配置的端口各自固定，未来 harness 必须使用原配置
或明确失败，不能改端口/删规则来适配。旧完整身份输入未被改写或简化。

## 证据与限制

新增原始证据：`private/p1-basic-launcher-validation/receipt.json`（207 条引用、51 个
测试输入）、`private/p1-basic-fixture-validation/acceptance.json`；对应独审逐项核对
冻结输入及引用，无当前范围内阻断。批准记录以不可变 hash 引用这些原件。

本次没有 VM/BPF、新来源进程证明、计量/质量验收、管理器死亡矩阵、真实注销/休眠、
非 Linux 运行或性能预算验收。T027–T034 的持久操作、协调器、恢复、CLI 与实际产品
生命周期均不由本组件结论替代；P1 和整个项目仍未完成。

root 负责状态机、发布/退出语义及最终范围裁决；executor 实际为 gpt-6.1-sol/medium，
负责已定范围测试与检查；scout 实际为 gpt-6-luna/high，独立核对证据，无子代理再派生。

## 稳定输入统一检查

192 项输入（190 个公开 Go 文件、go.mod、批准记录）在检查前后 hash 一致。
公开范围 internal/protocol/experiments/tests 的 gofmt、vet、test、race 和空白检查通过；
普通/race 各 410 顶层、1722 子节点，2111 pass、21 skip、0 fail。8 个空 helper 单列，
2103 个通过节点含断言；19 个 opt-in 与 2 个旧 owner visibility 环境跳过分列。
这轮没有真实核心、VM 或 BPF。首次检查只发现 approved.go 未 gofmt，修正格式后
完整执行；原失败和旧字节保留。T027 新源码在本轮输入释放后才开始，不计入此结果。

完整记录为 `private/p1-basic-required-checks/receipt.json`，SHA-256
`db90ba9c43863adfba0ad5b93501b61c3d509b549445d48e74c341eedd4a5b96`。
root 结合此前独立源码/实测审查接受 T026；任务共 140 项完成 40 项，下一项 T027
持久操作日志已进入实现，没有以组件测试代替 T034 产品矩阵。
