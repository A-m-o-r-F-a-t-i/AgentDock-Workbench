# Android 发布签名

## 信任边界

Android CI 候选继续使用临时 debug 身份，用于 lint、JVM、仪器测试和 API 26/33/34/35/37 模拟器验收。候选文件名包含 `test-signed`，只作为发布器输入，不进入 GitHub Release。

公开安装包由发布任务在所有 Android 验证完成后生成。`scripts/release/sign-android-release.py` 读取 `verification-android.json`，核对候选 APK 字节和 SHA-256，执行 `zipalign`，再使用固定发布密钥调用 `apksigner`。签名后必须满足 `packaging/android/release-signing.json` 中的证书指纹，发布器才接受 `AgentDock-Workbench-<version>-Android.apk`。

当前发布身份：

| 字段 | 值 |
| --- | --- |
| 密钥别名 | `agentdock-android-release` |
| 证书 SHA-256 | `ec58f290305b7281bf19825b7aa42835f1dd13240b759da90a680e2c3657eeb0` |

证书指纹属于公开身份信息。私钥和密码不得写入仓库、构建日志、Artifact 或 Release。

## GitHub Actions Secrets

仓库 Actions 需要配置以下三个 Secret：

| Secret | 内容 |
| --- | --- |
| `ANDROID_RELEASE_KEYSTORE_B64` | PKCS#12 keystore 的 Base64 文本 |
| `ANDROID_RELEASE_KEYSTORE_PASSWORD` | keystore 密码 |
| `ANDROID_RELEASE_KEY_PASSWORD` | 别名私钥密码 |

主发布流程与已验证构建恢复发布流程都从 Secret 注入签名材料。签名脚本在临时目录中解码 keystore，结束后由临时目录生命周期清理；密码只通过进程环境传给 `apksigner`，不会出现在命令行参数或报告中。

## 发布门禁

发布器要求同时存在：

1. `verification-android.json`，证明测试签名候选通过五个 API 等级和截图/导航验收。
2. 候选 APK 及精确 `.sha256` 旁车文件。
3. `verification-android-release.json`，记录固定证书指纹、公开 APK 摘要和来源候选摘要。
4. 公开 APK 及精确 `.sha256` 旁车文件。

公开签名报告中的 `source_apk` 必须与候选报告完全一致。证书指纹、来源摘要、公开 APK 摘要或报告身份任一不匹配，发布任务立即失败。

## 密钥保管与轮换

至少保留一份离线加密备份，并验证备份可以读取证书指纹。修改 `release-signing.json` 前必须先设计 Android 签名轮换方案并验证升级链；直接替换证书会使现有安装无法覆盖升级。

此前公开过的临时测试签名 1.1.9 与固定发布身份不同。设备从旧测试包迁移到固定签名包时需要保存重要配置、卸载旧包并重新安装一次；完成迁移后，后续版本保持同一发布身份即可覆盖升级。
