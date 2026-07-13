# 国内源调研与取舍

调研日期：2026-07-13。镜像状态会变化，provider 失败会返回明确错误，不会静默切到国外下载。

## JDK

| 发行版 | 结论 | 原因 |
|---|---|---|
| Eclipse Temurin | 支持，清华 TUNA `https://mirrors.tuna.tsinghua.edu.cn/Adoptium/` | 8/11/17/21/25；Linux/macOS/Windows；目录稳定可解析 |
| Alibaba Dragonwell | 支持，官方阿里 OSS | `https://dragonwell-jdk.io/releases.json` 提供中国大陆 OSS URL；Linux/Windows；x64/aarch64 依版本而定 |
| Huawei BiSheng | 支持，华为云 `https://mirrors.huaweicloud.com/kunpeng/archive/compiler/bisheng_jdk/` | 8/11/17/21；Linux x64/aarch64；同目录 SHA-256 |
| Tencent Kona | 暂不支持 | 官方站当前把二进制指向 GitHub Releases；未发现由官方承诺长期维护、覆盖版本与平台的普通 HTTP 国内制品源 |
| Oracle、Corretto、Zulu、Liberica、Microsoft、Semeru、SapMachine、GraalVM、JetBrains Runtime | 暂不支持 | 未发现符合上述标准的国内完整镜像；不能把 Maven 代理、容器镜像或第三方转存当作 JDK 制品源 |
| OpenJDK GA/RI | 暂不支持 | 华为云虽有 `openjdk/` 目录，但 LTS 线并未持续季度更新（例如 21 停在 21.0.2），不适合作为长期 JDK 版本源 |

Dragonwell 与 BiSheng 不覆盖 macOS，所以 macOS Java 使用 Temurin。Tencent Kona 本身稳定且支持主流平台，但“发行版稳定”和“国内二进制源稳定”是两项条件；CNB 镜像的 Release 明显落后于官方季度版本，后者未满足前不接入。

## Java 生态

| Candidate | 国内源 | 发现方式 |
|---|---|---|
| Gradle | `https://mirrors.cloud.tencent.com/gradle/` | 解析稳定版 `gradle-*-bin.zip` |
| Maven | `https://mirrors.aliyun.com/apache/maven/maven-3/` | 版本目录 → `binaries/` |
| Ant | `https://mirrors.aliyun.com/apache/ant/binaries/` | 当前 Apache 二进制目录 |
| Groovy | `https://mirrors.aliyun.com/apache/groovy/` | 稳定版本目录 → `distribution/` |
| JMeter | `https://mirrors.aliyun.com/apache/jmeter/binaries/` | 当前 Apache 二进制目录 |
| Tomcat | `https://mirrors.aliyun.com/apache/tomcat/` | 9/10/11 分支 → 当前版本 → `bin/` |
| Spring Boot CLI | `https://maven.aliyun.com/repository/central/org/springframework/boot/spring-boot-cli/` | Maven metadata + `*-bin.zip` |

Apache 镜像只保留当前受支持发布，不等价于永久历史归档。这符合“稳定源优先”目标，但旧版本可能从在线列表消失；已经安装的本地版本不受影响。

暂不接入 Scala、Kotlin、SBT、JBang、Grails、Micronaut、Quarkus、VisualVM、JMC 等：截至调研日未找到同时满足国内长期维护、稳定版完整、可脚本发现的二进制源。Kafka、Flink、Spark、Hadoop 虽有 Apache 镜像，但更像服务端发行包，通常需要集群配置；首版先控制范围。

## 与 SDKMAN 的对应

- `candidates/<name>/<version>` 保存解压版本。
- shell hook 解决子进程不能修改父终端环境的问题，实现 `use`。
- `defaults.json` 对应跨终端默认版本。
- `.jkvrc` 对应 SDKMAN `.sdkmanrc`。
- provider 直接读取国内镜像元数据，不依赖境外 broker。
- Go 单文件二进制替代 Bash-only 客户端，Windows 无需 WSL/MSYS。

## 完整性边界

BiSheng 使用镜像同目录 SHA-256。项目自身 Release 二进制由安装器校验 SHA-256。部分国内镜像不提供与制品同目录的校验文件；这些下载使用 HTTPS，但客户端会明确提示“未做内容哈希校验”。未来可维护一份签名索引，但该索引必须具备独立可信发布链，不能伪装成上游校验。
