# 事件源重规划：已送达事件持久化与身份探针

日期：2026-10-22。依据[ADR 005](../adr/005-telemetry-replan.md)，本轮已实现并实测接收可靠性；原身份/终末源完整性与质量/实际链仍未通过P0。脱敏机器摘要（本地原件 `telemetry-replan.summary.json`，不随Git上传）记录固定输入、结果和私有原始证据的SHA-256。

## 实际修复及验证

新增[Linux实验journal](../../experiments/telemetry/README.md)：每事件epoch/ID去重、保留整数原文、Write+Sync才确认、单writer锁、结果不确定poison/重开消歧、损坏/半帧拒绝且不截尾。9项顶层race测试通过。并接入既有GOST recorder探针；无SQL驱动或新代理协议依赖。

真实命令沿[实验说明](../../experiments/gost-mihomo/README.md)的固定Mihomo/GOST路径/hash，仅追加：

```text
--recorder-probe --recorder-journal --recorder-reject-first
```

实际构建可执行文件为 `/tmp/hoopa-p0-journal2`，私有运行目录 `/tmp/hoopa-p0-3253506149`。GOST启动后以实际core运行身份绑定source epoch和服务白名单；epoch由核心role/boot/PID/start/binary摘要/启动nonce的固定结构hash生成，不使用当前manager/collector run。原始payload、SID及metadata只存0700目录和0600文件。

| 子断言 | 实际结果 |
| --- | --- |
| 三路径受控校准 | 21/21通过 |
| 核心/metrics/数据认证 | 9/9通过 |
| 原生recorder decoded / 精确整数字节记录 | 20 / 20 |
| 完整落盘的唯一事件 | 20 |
| 落盘后故意ACK 503 | 1 |
| 关闭journal再重开，逐seq/epoch/ID/payload一致 | 20/20 |
| 原生重投/重复SID | 未观察到；去重由受控重复投递单元测试验证 |
| G01–G07完整门槛 | 仍全部unverified |

注入的503发生在Append+Sync之后，模拟接收方已保存但ACK失败。它与上一轮落盘前拒绝、或事件根本未送达不同。没有由此宣称GOST具有持久重试、源端final永不丢失、完整进程/域名归属或SQLite事务已经通过。

## 原入口身份实验

[sourceidentity](../../experiments/sourceidentity/README.md)只处理原始入口同UID、同netns、IPv4 loopback的ESTABLISHED TCP，按tuple/inode及持有FD进程核对PID/comm/start/boot；不转发、不提权。

共享FD拒绝、越界地址拒绝与本进程OS身份读取通过。预登记两个同名helper、各50连接的断言在本宿主失败于同UID进程FD目录不可读；初次失败已经保留，不能跳过不可见进程并声称唯一owner。探针现对该条件返回ErrUnknown+ErrVisibility，测试明确skip/unverified，100连接正确性没有通过。辅助helper入口本身通过不计为身份能力。

没有运行GOST admission→resolver接线；源码只证明admission提供转发前来源tuple，仍缺来源到SID/实际chain的整体证明。该分支不作为通用生产身份方案继续堆叠，转向ADR 005的同一数据面候选。其他FD传递/namespace/平台限制保留。

## 最终代码验证

固定Go1.27.3、GOTOOLCHAIN=local、模块代理关闭：gofmt/vet通过；全量race顶层46通过、2跳过、0失败，无数据竞争。跳过是默认真实core集成和100来源连接权限断言。sourceidentity/telemetry/config复用此前相同输入的Go测试缓存，gost-mihomo本轮实际执行；另有上述固定核心真实运行，未用skip冒充集成成功。epoch定义修正后仅重跑受影响Observer定向race（8项通过）、vet/build/fmt及真实固定核心20条回放，未重复全量。

日志：`/tmp/hoopa-telemetry-{fmt,vet,race}.log`、`/tmp/hoopa-sourceidentity-final.log`、`/tmp/hoopa-recorder-journal-{test,race,vet,build}.log`、`/tmp/hoopa-replan-final-{fmt,vet,race}.log`、`/tmp/hoopa-epoch-{race,vet,build,fmt}.log`。本轮没有真实断电、完整源spool、上游增强核心、生产SQL或跨平台实际运行；没有替换用户核心、开启系统代理/TUN、额外调用真实Agent。
