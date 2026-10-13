# GoFakeS3 S3 API 覆盖情况

> 基于当前仓库 HTTP 路由（`routing.go`）、请求处理（`gofakes3.go` / `uploader.go`）与 Backend 接口（`backend.go`）整理。
>
> 对照来源：[Amazon S3 API Operations](https://docs.aws.amazon.com/AmazonS3/latest/API/API_Operations_Amazon_Simple_Storage_Service.html)（Amazon S3 数据面操作，不含 S3 Control / Outposts / Tables / Vectors / Files）。
>
> 生成日期：2026-10-13

## 概览

| 类别 | 已实现 | 桩/部分 | 未实现 |
|------|--------|---------|--------|
| Service | 1 | 0 | 1 |
| Bucket | 9 | 2 | ~50 |
| Object | 8 | 2 | ~19 |
| Multipart | 6 | 0 | 1 |
| **合计（约）** | **~24** | **~4** | **~71** |

说明：

- **已实现**：有明确路由与处理逻辑，可按 S3 语义使用。
- **桩/部分**：路由接受请求但不真正执行语义，或依赖特定 Backend / 行为不完整。
- **未实现**：无对应 query/subresource 路由，通常落到错误路径或误当作普通对象操作。

---

## 一、已实现的 API

### 1. Service

| API | HTTP | 说明 |
|-----|------|------|
| `ListBuckets` | `GET /` | 列出所有 bucket |

### 2. Bucket

| API | HTTP | 说明 |
|-----|------|------|
| `CreateBucket` | `PUT /{bucket}` | 校验 bucket 名；已存在返回 `BucketAlreadyExists` |
| `DeleteBucket` | `DELETE /{bucket}` | 仅空桶可删；支持 MinIO 扩展头 `x-minio-force-delete: true` |
| `HeadBucket` | `HEAD /{bucket}` | 检查 bucket 是否存在 |
| `ListObjects` | `GET /{bucket}` | V1；支持 prefix / delimiter / marker / max-keys |
| `ListObjectsV2` | `GET /{bucket}?list-type=2` | V2；continuation-token 由 NextMarker base64 伪装实现 |
| `GetBucketLocation` | `GET /{bucket}?location` | 固定返回空 LocationConstraint |
| `GetBucketVersioning` | `GET /{bucket}?versioning` | 需 Backend 实现 `VersionedBackend`（当前仅 `s3mem`） |
| `PutBucketVersioning` | `PUT /{bucket}?versioning` | 同上；MFA Delete 未真正支持 |
| `ListObjectVersions` | `GET /{bucket}?versions` | 需 `VersionedBackend` |
| `DeleteObjects` | `POST /{bucket}?delete` | 批量删除；有版本时走 `DeleteMultiVersions` |
| `PostObject`（浏览器表单上传） | `POST /{bucket}` | multipart/form-data；非独立官方 Action 名，常用能力 |

### 3. Object

| API | HTTP | 说明 |
|-----|------|------|
| `GetObject` | `GET /{bucket}/{key}` | 支持 Range、`If-None-Match`、`If-Modified-Since`、`response-*` 响应头覆盖、`versionId` |
| `HeadObject` | `HEAD /{bucket}/{key}` | 支持 Range（206）；`versionId` 路由存在，但 handler 当前未按版本取对象 |
| `PutObject` | `PUT /{bucket}/{key}` | 支持 metadata、`Content-MD5`、aws-chunked、条件写 `If-Match` / `If-None-Match` |
| `CopyObject` | `PUT` + `x-amz-copy-source` | 同/跨 bucket 复制；不支持源 `versionId` |
| `DeleteObject` | `DELETE /{bucket}/{key}` | 开启版本时写入 delete marker |
| `DeleteObject`（指定版本） | `DELETE /{bucket}/{key}?versionId=` | 永久删除指定版本；需 `VersionedBackend` |
| `GetObject`（指定版本） | `GET /{bucket}/{key}?versionId=` | 需 `VersionedBackend` |
| `RestoreObject` | `POST /{bucket}/{key}?restore` | `GLACIER`/`DEEP_ARCHIVE`；异步（`WithRestoreDelay`，默认 1s）；`Days` 默认 1..365（`WithRestoreMaxDays`）；续期默认 extend（`WithRestoreRenewal`）；未恢复 Get/Copy 返回 `InvalidObjectState`；`DEEP_ARCHIVE` 拒绝 Expedited |

### 4. Multipart Upload

默认由内存 `uploader` 实现；Backend 可选实现 `MultipartBackend` 覆盖。

| API | HTTP | 说明 |
|-----|------|------|
| `CreateMultipartUpload` | `POST /{bucket}/{key}?uploads` | |
| `UploadPart` | `PUT /{bucket}/{key}?uploadId=&partNumber=` | |
| `CompleteMultipartUpload` | `POST /{bucket}/{key}?uploadId=` | |
| `AbortMultipartUpload` | `DELETE /{bucket}/{key}?uploadId=` | |
| `ListMultipartUploads` | `GET /{bucket}?uploads` | |
| `ListParts` | `GET /{bucket}/{key}?uploadId=` | |

### 5. 附带能力（非独立 API，但影响兼容性）

| 能力 | 状态 |
|------|------|
| Path-style / Virtual-hosted 寻址 | 支持（`WithHostBucket` / `WithHostBucketBase`） |
| HTTP CORS 中间件 | 支持（服务端全局 CORS，**不是** `PutBucketCors` API） |
| 对象元数据 `x-amz-meta-*` | 支持 |
| StorageClass（含 `STANDARD`/`STANDARD_IA`/`GLACIER`/`DEEP_ARCHIVE`/`GLACIER_IR` 等） | 支持（Put/Copy/Multipart；List/Head/Get 回显；Copy 未指定时目标 `STANDARD` 且不继承 restore；`GLACIER`/`DEEP_ARCHIVE` 需 Restore；`GLACIER_IR` 立即可读） |
| 条件 PUT（`If-Match` / `If-None-Match`） | 支持 |
| 字节 Range 读写 | 支持（多 Range 不支持） |
| aws-chunked 上传 | 支持 |
| 鉴权 / 签名校验 | **不校验**（任意 AccessKey 可用） |

---

## 二、桩实现 / 部分实现

| API | 状态 | 说明 |
|-----|------|------|
| `PutObjectAcl` / `GetObjectAcl` / `PutBucketAcl` / `GetBucketAcl` 等 `?acl` | 桩 | `routing.go` 对 `?acl` 直接返回 `200 OK`，不读写 ACL、不覆盖对象体 |
| `PutBucketVersioning`（MFA Delete） | 部分 | 可开关 Enabled/Suspended；MFA Delete 未真正实现 |
| `HeadObject` + `versionId` | 部分 | 路由会进入 version 分支，但 `headObject` 未使用 `versionID` 参数 |
| `CopyObject` + 源版本 | 未完成 | 注释标明不支持源 `versionId` / delete marker |
| `GetObjectTagging` / `PutObjectTagging` | **未实现** | 无 `?tagging` 路由；请求会落入普通 `GetObject`/`PutObject`，可能误覆盖对象体。存在相关测试，但非专用 tagging 语义 |

---

## 三、未实现的 API（按类别）

对照官方 Amazon S3 Operations 列表，以下均未在路由层实现。

### 1. Multipart 缺失

| API | 说明 |
|-----|------|
| `UploadPartCopy` | 从已有对象复制分片 |

### 2. Object 相关

| API |
|-----|
| `GetObjectAcl`（真实语义） |
| `PutObjectAcl`（真实语义） |
| `DeleteObjectTagging` |
| `GetObjectTagging` |
| `PutObjectTagging` |
| `GetObjectAttributes` |
| `GetObjectLegalHold` / `PutObjectLegalHold` |
| `GetObjectLockConfiguration` / `PutObjectLockConfiguration` |
| `GetObjectRetention` / `PutObjectRetention` |
| `GetObjectTorrent` |
| `SelectObjectContent` |
| `RenameObject` |
| `UpdateObjectEncryption` |
| `WriteGetObjectResponse` |
| `GetObjectAnnotation` / `PutObjectAnnotation` / `DeleteObjectAnnotation` / `ListObjectAnnotations` |

### 3. Bucket 配置 / 策略 / 安全

| API |
|-----|
| `GetBucketAcl` / `PutBucketAcl`（真实语义） |
| `DeleteBucketCors` / `GetBucketCors` / `PutBucketCors` |
| `DeleteBucketEncryption` / `GetBucketEncryption` / `PutBucketEncryption` |
| `DeleteBucketLifecycle` / `GetBucketLifecycle` / `GetBucketLifecycleConfiguration` / `PutBucketLifecycle` / `PutBucketLifecycleConfiguration` |
| `DeleteBucketPolicy` / `GetBucketPolicy` / `GetBucketPolicyStatus` / `PutBucketPolicy` |
| `DeleteBucketReplication` / `GetBucketReplication` / `PutBucketReplication` |
| `DeleteBucketTagging` / `GetBucketTagging` / `PutBucketTagging` |
| `DeleteBucketWebsite` / `GetBucketWebsite` / `PutBucketWebsite` |
| `GetBucketLogging` / `PutBucketLogging` |
| `GetBucketNotification` / `GetBucketNotificationConfiguration` / `PutBucketNotification` / `PutBucketNotificationConfiguration` |
| `GetBucketRequestPayment` / `PutBucketRequestPayment` |
| `GetBucketAccelerateConfiguration` / `PutBucketAccelerateConfiguration` |
| `GetBucketOwnershipControls` / `PutBucketOwnershipControls` / `DeleteBucketOwnershipControls` |
| `GetPublicAccessBlock` / `PutPublicAccessBlock` / `DeletePublicAccessBlock` |
| `GetBucketAbac` / `PutBucketAbac` |
| `CreateSession` |

### 4. Analytics / Inventory / Metrics / Intelligent-Tiering

| API |
|-----|
| `GetBucketAnalyticsConfiguration` / `PutBucketAnalyticsConfiguration` / `DeleteBucketAnalyticsConfiguration` / `ListBucketAnalyticsConfigurations` |
| `GetBucketInventoryConfiguration` / `PutBucketInventoryConfiguration` / `DeleteBucketInventoryConfiguration` / `ListBucketInventoryConfigurations` |
| `GetBucketMetricsConfiguration` / `PutBucketMetricsConfiguration` / `DeleteBucketMetricsConfiguration` / `ListBucketMetricsConfigurations` |
| `GetBucketIntelligentTieringConfiguration` / `PutBucketIntelligentTieringConfiguration` / `DeleteBucketIntelligentTieringConfiguration` / `ListBucketIntelligentTieringConfigurations` |

### 5. Metadata / Directory / 其他较新能力

| API |
|-----|
| `CreateBucketMetadataConfiguration` / `GetBucketMetadataConfiguration` / `DeleteBucketMetadataConfiguration` |
| `CreateBucketMetadataTableConfiguration` / `GetBucketMetadataTableConfiguration` / `DeleteBucketMetadataTableConfiguration` |
| `UpdateBucketMetadataAnnotationTableConfiguration` |
| `UpdateBucketMetadataInventoryTableConfiguration` |
| `UpdateBucketMetadataJournalTableConfiguration` |
| `ListDirectoryBuckets` |

---

## 四、Backend 能力矩阵

| 能力 | `s3mem` | `s3bolt` | `s3afero`（multi） | `s3afero`（single） |
|------|---------|----------|--------------------|---------------------|
| 基础 CRUD | ✅ | ✅ | ✅ | ✅ |
| List 分页 | ✅ | ✅ | ⚠️ 可返回未实现分页 | ⚠️ 可返回未实现分页 |
| Versioning（`VersionedBackend`） | ✅ | ❌ | ❌ | ❌ |
| Multipart（`MultipartBackend`） | ❌（走内存 fallback） | ❌（fallback） | ❌（fallback） | ❌（fallback） |
| CreateBucket / DeleteBucket | ✅ | ✅ | ✅ | ❌（`NotImplemented`） |
| Range Get | ✅ | ✅ | ✅ | ✅ |
| 条件 Put | ✅ | ✅ | ✅ | ✅ |

> 无 `VersionedBackend` 时：`PutBucketVersioning` / `ListObjectVersions` / 指定 `versionId` 的读写删会返回 `NotImplemented`（Get/Put versioning 在未启用过版本时 Get 可返回空配置）。

---

## 五、常用开发场景对照

| 场景 | 是否可用 | 备注 |
|------|----------|------|
| 本地 Put/Get/Delete 对象 | ✅ | 核心路径完整 |
| ListObjects / ListObjectsV2 | ✅ | |
| 分片上传（AWS SDK Uploader） | ✅ | 内存暂存 parts |
| 复制对象 | ✅ | 无源版本号 |
| Bucket 版本控制 | ⚠️ | 仅 `s3mem` |
| 浏览器直传（POST form） | ✅ | |
| 对象/桶 ACL | ⚠️ | 仅 no-op 接受 |
| 对象 Tagging | ❌ | |
| Bucket Policy / CORS API / 加密配置 | ❌ | |
| Lifecycle / Replication / Notification | ❌ | |
| Object Lock / Legal Hold | ❌ | |
| Glacier RestoreObject | ✅ | Days 默认 1..365（可配 max）；异步 delay/Tier；DEEP_ARCHIVE 拒绝 Expedited；续期 extend 或 from-now |
| Presigned URL 鉴权校验 | ❌ | 签名不校验 |
| UploadPartCopy | ❌ | |

---

## 六、实现入口索引

| 文件 | 职责 |
|------|------|
| `routing.go` | HTTP 方法 + query 分发 |
| `gofakes3.go` | 各 API handler |
| `uploader.go` | 默认 multipart 实现 |
| `backend.go` | `Backend` / `VersionedBackend` / `MultipartBackend` 接口 |
| `backend/s3mem/` | 内存 Backend（含版本） |
| `backend/s3bolt/` | BoltDB 持久化 |
| `backend/s3afero/` | 文件系统 Backend |
| `cors.go` | 服务端 CORS 中间件（非 S3 CORS API） |

---

## 七、小结

当前 GoFakeS3 覆盖了 **本地开发/测试最常用的对象存储核心面**：

- Bucket：创建 / 删除 / Head / List（V1+V2）/ Location
- Object：Put / Get / Head / Delete / Copy / 批量删除
- Multipart：完整六件套（缺 `UploadPartCopy`）
- Versioning：仅内存 Backend 完整支持

相对完整 AWS S3 API，**大量 Bucket 配置类、安全策略类、分析库存类、Object Lock / Tagging / Select 等高级能力均未实现**。仓库定位为测试假服务器，上述缺口符合其非生产用途声明。
