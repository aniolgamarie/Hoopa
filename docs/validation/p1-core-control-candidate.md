# T026 基础控制候选：组件与真实转发有限验收

2026-10-27。核心候选的基础启动门控已取得有限证据，**T026 尚未完成，P1 尚未交付**。
管理器的固定能力加载、持久登记后激活、真实管理器崩溃接管仍须继续接线。
机器索引：[core-basic-control-candidate-manifest.json](../../experiments/mihomo-events/core-basic-control-candidate-manifest.json)。

## 固定输入与变化

新二进制 SHA-256 为 `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。
沿用固定上游 v1.19.26、commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`。
本次未使用 ldflags，内置版本仍为 `1.10.0`；它不是上游源码版本，也不替代二进制 hash。
这是 Mihomo 核心制品，大小不用于声称 Hoopa 管理器自己的二进制预算通过。

在历史 T016 源树外建立新副本；相对它只有7个文件变化：两处分派文件、新 basic
runtime、两份共享纯协议源码和两份测试。既有 BPF、原始进程归属、来源计数/恢复、
ready/线路和规则算法源码未改。未设置新变量时旧路径保持；新变量出现就选择新路径，
冲突 CLI 模式在早退之前拒绝，不能落回 stock 启动。

[0018](../../experiments/mihomo-events/patches/0018-multi-entry-owners.patch) 是原 T016 v2
delta 的逐字文本发布；没有改变或重评其历史制品。
[0019](../../experiments/mihomo-events/patches/0019-basic-control-v1.patch) 是本次增量。
从固定 T140 reviewed-source 复制全新树，实际应用0018/0019后，1012个文件的字节
与权限等于测试源树；在该 fresh 树离线构建，二进制 hash 与已测试候选完全相同。
既有001–0017补丁、所有旧二进制和失败原件保持。二进制与原始私有材料不进入Git。

## 控制契约与原语检查

[控制 v1](../../specs/001-hoopa-proxy-manager/contracts/core-control-v1.md) 限定 basic
受保护配置、controller 认证、独立 RunID、完整快照以及 activate/drain 单次槽位。
controller 可先观察，app socket 保持 bind/未 listen；全部 Listen 成功之前不启动
任何 handler。失败关闭本核心所有 app FD，进入终末 degraded，不能换 ID 再开放。
幂等结果为历史回执，不能替代当前状态或让 draining 回到 open。

纯协议初轮7顶层/127子测试通过后，独审指出状态可达性校验过宽。root修订为36种
组合中8合法/28拒绝，保留初轮原件；新版定向 race 为7顶层/159子节点、0失败/跳过，
vet通过。核心使用与公共协议逐字相同的源码，不维护另一套宽松解码。

核心新测试为7顶层/54子节点，均通过；包括原包的定向 race 共40顶层/392子节点，
430节点通过、0失败、2个旧 socket opt-in 跳过，vet通过。不能将父/中间节点当作
独立场景或核心启动分母；未为增加分母重跑旧矩阵。

真实 socket 验证了初始 SO_ACCEPTCONN=0、认证后开放、drain 后旧已接纳连接继续
完成 SOCKS 认证、新 connect 被拒。第二端口的自有竞争 listener 强制 Listen 失败；
本核心 app socket inode 无 FD 残留，竞争者继续回显。并发 activate/drain/stop 在有限
调度中满足允许结果，race无报告。Close 返回错误的一例是“真实关闭后注入错误”，
不冒充真实内核 Close 故障。

partial Listen 窄窗口的并发探针本轮记录0次成功握手、0应用字节；**没有实际捕捉
到该窗口的已连接样本**。失败后关闭与竞争者存活有实测，全 Listen 前无 handler
的顺序由稳定代码及独审支持，不宣称跨端口 TCP 握手原子性。

## 一次真实固定核心运行

另有1次普通宿主账户直接运行固定二进制，未使用产品 adapter、BPF 或 VM：

- controller 状态为 closed 时，两 app 入口均拒绝 connect；完整实际快照与独立
  预期逐字节一致，错误认证、旧端点、query及错误配置绑定被拒。
- 正确 activate 及相同请求重投返回一致结果；HTTP CONNECT 和 SOCKS5 各1条真实
  流，分别经过 group:US / group:JP 对应的本地 HTTP 上游 A / B，完成回显。
- drain 后两条旧流继续传输，新 app connect 被拒；再次提交原 activate 只返回
  历史 opened，当前仍 draining，入口未重开。
- 自有核心收到 SIGTERM 后正常退出0；输入二进制哈希前后相同，没有停止外部服务。

该 run 保存配置、独立预期/实际快照、最终状态、脚本断言和路由观察；没有保存逐个
API 原始请求/响应字节。BPF 未加载是受限分支与 harness 记录，不是内核 link inventory
审计。它不证明 Hoopa 管理器的出生、持久登记、死亡或接管；这些边界不能冒领。

## 受影响门槛与下一步

G04 在本次新增门控/排空/关闭路径上取得有限实际证据；管理器恢复仍待产品接线。
G05 覆盖完整基础快照和两个组的真实转发，不扩展为全部订阅协议、自动选优或动态
选择 API。G07 覆盖受保护入口与有限认证/拒绝路径；管理器同连接证明后的真实认证
仍在实现。G01–G03既有算法未改且 basic 模式不启用其身份/计量能力，因此不复制旧
hash 的正式通过到新 hash；完整身份模式将另行取得所需当前制品证据。

root接受本候选的组件范围并继续T026；不发布正式 CapabilityRecord、不更换P1产品
fixture、不勾选整个任务。executor gpt-6.1-sol/medium 实现纯协议与限定核心测试，
scout gpt-6-luna/high 独立审查，root负责状态机、纠错、真实转发及最终范围裁决。
