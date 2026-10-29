# US3：来源持久化与初始化准备

2026-10-28，T043 进行中。机器索引（本地原件 `../../experiments/gost-mihomo/us3-source-storage-validation.json`，不随Git上传）记录已完成的恢复解析、不可变资产、私有设置及初始化守卫模块。当前尚无完整 subscription.update 事务或服务命令验收，不勾选 T043；总体仍为 56/140。

`subscription.RestoreAsset` 不信任文件里的节点 ID、来源、地区或 parser_version。它要求调用方提供原身份密钥、来源 ID 和完整本地地区规则，重新解析原始订阅，再逐字核对全部私有资产。缺密钥、换密钥、修改未匹配的地区规则、重复字段或变更序列化形式均拒绝，失败不返回部分 Snapshot。纯模块普通/race 各 **56 PASS，0 跳过**，包含原身份 golden 回归。

`Store.PublishSourceAsset` 将有界私有 JSON 按完整 SHA256 写入独立的不可变文件；文件 fsync 后 rename，再 fsync 父目录。重复发布相同字节也重新建立文件和目录持久性屏障。rename 后无法确认持久性时标记 writer 不确定，不能继续写或把可见文件当成已确认资产。读取不创建、同步、修复或清理文件。这个存储层允许 opaque JSON，本身不授予节点身份、候选成功或运行权限；只有后续完整 update receipt 才能把资产标为 latest/pending。

资产存储普通/race 各 **33 PASS**。实际写入了一个恰好 64 MiB 的 JSON 文件，并验证超限零写；1024 文件和 512 MiB 总量边界使用稀疏文件测试元数据约束，不代表真实积累了 512 MiB 订阅。空 staging 文件仍是未完成写入，阻止新资产发布，既不自动删除也不变成候选。新增持久目录同时加入 Paths 的保留范围，拒绝与 Runtime 相等或嵌套；最终 Paths+SourceAsset 定向 race **115 PASS、0 跳过**，包括拒绝前零文件变化。

私有设置存储原语只写一个有界文件，同字节重试可同步，不同现存字节冲突。其普通/race 各 **29 PASS**。上层 `SourceSettings` codec 使用完整精确 JSON、32 字节原密钥和既有 sourceprofile 校验器验证 controller/入口设置；getter 返回副本，格式化隐藏私有材料。codec 普通/race 各 **76 PASS**，独立 Python 摘要向量一致。

`Coordinator.InitializeSourceSettings` 是未来 init 命令要调用的本地原语。它在既有锁内复核 revision；对于已有合法相同记录只做持久性重同步。记录缺失时，检查已发布配置、原操作历史以及来源资产/临时文件，发现原件就报恢复所需，不生成新密钥。已压缩的 source/apply 操作身份仍会阻止误初始化；已有规则禁止压缩的 runtime/service 操作没有被伪造为 tombstone。只读取不到某个文件，不足以判定初次使用。

初始化 control 普通/race 各 **31 PASS**；只读 inventory 各 **19 PASS**；相关 vet 通过。测试覆盖实际文件删除、损坏、权限/链接拒绝、取消、共享锁等待、跨重开精确重试及拒绝零写。Journal 原件由组件夹具构造，这些测试不冒充真实订阅更新。也不声称能识别同 UID 主体同时删除密钥及所有独立历史的情况。

早期两类测试失败已保留：恢复解析夹具使用了错误的地区 kind；初始化夹具尝试压缩不允许压缩的生命周期记录。修正均遵循已有实现语义，没有为了通过测试放宽生产边界。各批次按自己的源码 pin 记录，不合并宣称一次全树执行；最终独立复核与主代理验收一致。executor 为 gpt-6.1-sol/medium，scout 为 gpt-6-luna/high。

下一步按[更新契约](../../specs/001-hoopa-proxy-manager/contracts/subscription-update-v1.md)接入冻结 HTTPS 下载输入、网络请求前的持久预约、完成时的来源/凭据/revision 复核、原 Journal 恢复与 CLI。候选下载不会推进 global revision，也不会自动替换核心。当前测试是实际文件系统与注入故障边界，未模拟断电；尚未交付完整 init CLI、持久调度、候选清理、来源切换产品矩阵或其他平台支持。
