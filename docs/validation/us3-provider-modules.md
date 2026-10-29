# US3 来源读取、解析与稳定身份（T038/T039）

2026-10-28 接受有限模块实现，任务总进度53/140。来源更新/选择的产品接线、运行配置编译和两来源实际线路矩阵仍待T040–T044；本记录不代表US3或P1整体完成。

显式Fetch支持Linux本地普通文件、私有URL引用指向的HTTPS。默认单次30秒、编码与解码各10MiB；请求取消、非200、无效gzip、超限或跨源/降级重定向均失败并且不返回部分候选。最多3次同HTTPS origin重定向。URL引用有16KiB上限，必须为当前用户私有普通文件，不接受最终symlink/FIFO/hardlink。文件从同一描述符读取并检查检测到的大小/mtime/ctime变化；不声称能够强制终止阻塞的远程文件系统读。默认Transport独立构造、启用证书验证、Proxy为空；明确传入的Transport由可信调用方负责。路径不做URL解码，URI形态的路径按严格Intent规则拒绝。Fetch不发布快照、不推进revision，不把下载成功称作生效。

Parse仅接受一个文档的proxies数组，拒绝未知/重复键、危险tag、隐含全局策略或线路控制。默认10000节点、深度64、64次alias与1000000展开值；安全alias可用，超限返回固定错误。空数组可形成空快照，但不能因此绕过必需组校验。显式地区前缀规则提供标签证据；未声明为unknown，冲突为ambiguous，不推断地理位置。

有限协议profile包含SOCKS5、HTTP、SS四种AEAD、Trojan TCP/WS、VMess AEAD alterId=0的TCP/WS、VLESS基础TCP/WS、Hysteria2固定端口。并非上游全部参数兼容：plugin、Reality、gRPC/H2/XHTTP、端口跳跃、附加QUIC参数、系统路由/interface/dialer代理、任意headers、证书文件及关闭TLS校验均不接受；遇到不支持字段/选项，整份候选失败，不删去节点继续运行。支持HY2传输不等于承诺UDP/QUIC应用计量。

归一化保持固定核心已确认的默认含义：Trojan TCP ALPN默认h2/http1.1，TLS WS仅http1.1；HY2省略ALPN时不产生空数组覆盖原默认。VMess/VLESS TLS WS使用显式servername、无端口WS Host、server的优先级；Host含端口时要求显式servername。WS路径中的ed参数可触发核心早期数据，因此连同编码/重复形式一起拒绝。固定核心-t不观察真实握手，这些默认语义同时依据固定源码核验与具体回归断言。

节点ID为版本化规范连接参数与source ID的HMAC-SHA256，必须显式提供32字节私有键；显示名/地区不参与。认证、协议参数、端点、来源或键变化产生新身份；同名可并存、同一连接身份重复则拒绝。固定pin仅按来源+ID解析，绝不改绑同名或其他来源。键不会隐式生成/更换；生命周期持久化另由后续集成实现。Snapshot把私有原文/规范连接与公开metadata绑定，篡改公开字段后不能编译或解析pin；敏感材料仅由显式Sensitive访问器返回独立副本，普通JSON/格式化和错误不含认证材料。编码金向量用独立Python计算固定。

验证：解析/身份普通与race各157通过；实际本地TLS/文件Fetch普通与race各16通过；均0失败/0跳过，包vet通过。已固定SHA的Mihomo对34组原始/规范配置共68次离线-t全通过，二进制前后摘要不变；没有远端握手、转发或真实ALPN协商证据。各批次及源码边界见验证索引（本地原件 `../../experiments/gost-mihomo/p1-source-module-validation.json`，不随Git上传）。修正前收据和原设计保留；最终Fetch仅有注释澄清，不混称单次全树执行。

root负责方案修订、Fetch实现、固定核心验证及最终审查；executor实际gpt-6.1-sol/medium负责解析/身份，scout实际gpt-6-luna/high负责固定源码与Fetch独立审查。原始凭据均为合成测试数据，二进制/私有日志保留在忽略目录。
