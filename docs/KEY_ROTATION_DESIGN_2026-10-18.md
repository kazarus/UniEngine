# UniEngine 加密/验签密钥轮换与版本化方案（待拍板）

> 状态：设计稿，未实现
> 日期：2026-10-18
> 目标：在不破坏现有 `ENC:` / `MAC:` 数据的前提下，引入 **key id + 算法版本 + 多密钥并存**，支持在线轮换、灰度迁移和旧密钥退休。

---

## 1. 现状与问题

### 1.1 加密

当前 `ENC:` 载荷：

```text
ENC: base64( salt[16] + iter[4] + nonce + AES-256-GCM(ciphertext+tag) )
```

- 密钥来自 `SecretBy`，PBKDF2-HMAC-SHA256 派生；
- `SecretIter` 控制迭代数；
- 密文只携带盐与迭代数，**没有 key id / 算法版本**；
- 轮换 `SecretBy` 后，旧密文只能用旧密钥解，但引擎不知道“这条密文属于哪把密钥”；
- 只能整库停机切换或应用层自行判断。

### 1.2 验签

当前 `MAC:` 载荷：

```text
MAC: base64(HMAC-SHA256(VerifyBy, payload))
payload = "UniMac" + table + columnCount + 每列(name + valueLen + value)
```

- 密钥来自 `VerifyBy`，没有 key id；
- 轮换 `VerifyBy` 后旧 MAC 无法验证；
- `VerifyHook` 只能拿到 payload，拿不到“应该用哪把 key”。

### 1.3 已删除的旧机制

`VerifyMacVer` 与 MAC v1 已删除，当前只有一种 MAC 载荷。  
本方案**不重新引入全局版本开关**，版本信息放在每条密文/MAC 的值内。

---

## 2. 设计目标与非目标

### 目标

1. 一条密文/MAC 自描述：算法、key id、KDF 参数、salt/nonce、密文/MAC。
2. 多密钥并存：新写用 active key，旧读按 key id 找 key。
3. 支持在线轮换：先加新 key，再迁移数据，最后退休旧 key。
4. 未知 key id / 未知算法必须显式报错，不能静默降级。
5. 保持 `ENC:` / `MAC:` 标签不变，避免数据库列格式大迁移。
6. 兼容当前无 key id 的旧格式，作为 legacy key 读取。

### 非目标

- 不实现 HSM/KMS 客户端；通过 hook 交给应用。
- 不做自动后台迁移任务；迁移由应用按批驱动（可复用 `SelectL`/`Update`）。
- 不改变 `SetVerify` 的主键冻结、保护列覆盖等既有语义。
- 不重新引入 `VerifyMacVer` 这类全局开关。

---

## 3. 推荐方案：值内 key id + 多密钥环

### 3.1 配置结构

新增密钥环（建议放在引擎配置，不放在每张表）：

```go
type TSecretKey struct {
    ID       string // 唯一 id，建议 UUID/ULID；"legacy" 保留
    Secret   string // base64 或高熵字符串；不打印
    Iter     int    // PBKDF2 迭代数；0=默认
    RetireAt time.Time // 可选：只用于运维提示，不参与解密
}

type TVerifyKey struct {
    ID       string
    Secret   string
    Alg      string // "HMAC-SHA256"；预留 "HMAC-SM3"
    RetireAt time.Time
}

type TUniEngine struct {
    // ...既有字段...

    SecretKeys        []TSecretKey
    ActiveSecretKeyID string
    VerifyKeys        []TVerifyKey
    ActiveVerifyKeyID string

    // 兼容现有单密钥配置：
    // SecretBy/SecretIter/VerifyBy 仍可用，映射为 id="legacy"
    SecretBy   string
    SecretIter int
    VerifyBy   string
}
```

规则：

- `SecretKeys` 为空：完全走现有 `SecretBy`，写入保持旧 `ENC:` 格式；
- `SecretKeys` 非空：`ActiveSecretKeyID` 必填，新写入用 active key；
- `VerifyKeys` 同理；
- key id 去重；重复直接 `Initialize` 失败；
- active key 不存在、密钥为空、迭代数越界，`Initialize` fail-fast；
- 密钥环按**不可变快照**使用；运行中轮换通过重新赋值整份 slice，不在原 slice 上原地改。

### 3.2 加密格式 v2（标签仍为 `ENC:`）

```text
ENC: base64(
    magic[4] = "UEK1"
    keyIDLen[1]
    keyID[keyIDLen]
    alg[1]          // 1 = AES-256-GCM
    iter[4]
    salt[16]
    nonce[12]
    ciphertext+tag
)
```

- `magic` 用于区分旧 `ENC:`：解码后以 `UEK1` 开头即新格式；否则按旧格式读取；
- `keyID` 最大 255 字节，建议 ≤ 32；
- `alg` 预留算法敏捷；当前只支持 AES-256-GCM；
- `iter` 为该 key 的 PBKDF2 迭代数；
- **AAD** 使用 `magic + keyID + alg + iter + salt`，把 key id/算法/参数纳入认证，防止头部被替换；
- salt 每次写入随机；nonce 每次写入随机；
- 缓存键必须包含 `keyID + keyFingerprint + salt + iter`，不能只用 salt/iter。

旧格式继续读：

```text
ENC: base64(salt + iter + nonce + ciphertext+tag)  // 用 SecretBy 派生
```

新写入在 `SecretKeys` 非空时一律 v2；旧写入不再产生。

### 3.3 MAC 格式 v2（标签仍为 `MAC:`）

```text
MAC: base64(
    magic[4] = "UEM1"
    keyIDLen[1]
    keyID[keyIDLen]
    alg[1]          // 1 = HMAC-SHA256
    mac[32]
)
```

- MAC 计算的输入显式绑定 key id 与算法：

```text
mac = HMAC(key, "UEM1" | keyID | alg | payload)
```

这样即使攻击者改 envelope 里的 key id，也会因 HMAC 输入变化而验证失败；
- 旧 `MAC:` 无 magic 的 base64 继续按 legacy `VerifyBy` 读；
- 新写入在 `VerifyKeys` 非空时一律 v2；
- `VerifySignRow` 使用 active verify key 生成 v2。

### 3.4 钩子

当前 hook 签名没有 key id，无法参与轮换。

建议新增可选 hook，旧 hook 继续兼容：

```go
type TSecretKeyHook func(KeyID, Value string, Encrypt bool) (string, error)
type TVerifyKeyHook func(KeyID string, Payload []byte) ([]byte, error)

// 新字段
SecretKeyHook TSecretKeyHook
VerifyKeyHook TVerifyKeyHook
```

规则：

- `SecretKeyHook` 非空时，引擎只负责 envelope 组装/解析，密钥实际使用交给应用（HSM/KMS）；
- `VerifyKeyHook` 同理，引擎传入 key id 和已绑定 key id 的 payload；
- 旧 `SecretHook`/`VerifyHook` 视为 `legacy` key 的 hook；
- 新旧 hook 同时设置时 `Initialize` 报错，避免歧义。

> 如果短期不做 hook 扩展，可先只支持内置密钥环；这一步作为 P2。

---

## 4. 轮换流程

### 4.1 加密轮换（在线）

1. **加新 key**
   - 配置中加入 `KeyB`，`ActiveSecretKeyID=KeyB`，保留 `KeyA`；
   - 部署后：读请求能解 KeyA/KeyB，写请求全部写 KeyB。
2. **观察**
   - 确认写入的 `ENC:` 解码后 key id 为 KeyB；
   - 监控 unknown key id / 解密失败为 0。
3. **迁移旧数据**
   - 按主键分批 `SelectL` 读旧行 → 解密 → `Update` 写回；
   - 必须在同一事务/同一行版本控制下做，避免覆盖并发更新；
   - 对加密列是 `SetVerify` 保护列的表，**先重加密，再重算 MAC**，否则 MAC 与密文不一致。
4. **确认**
   - 统计 `ENC:` 中 key id=KeyA 的行数，降为 0；
   - 保留 KeyA 一个回滚窗口。
5. **退休**
   - 从 `SecretKeys` 移除 KeyA；
   - 保留 `KeyA` 的离线备份；此后旧密文无法解。

### 4.2 验签轮换（在线）

1. 加 `VerifyKeyB`，`ActiveVerifyKeyID=VerifyKeyB`，保留 A；
2. 新写入/更新全部用 B；
3. 后台重签：
   - 读出完整行 → `VerifySignRow`（此时 active=B）→ 更新 MAC 列；
   - 或用 `Update` 重写整行（会触发签名）。
4. 统计 `MAC:` key id=A 的行数，降为 0；
5. 移除 A；严格模式确认无 legacy MAC。

### 4.3 加密 + 验签同时启用

顺序必须是：

```text
读旧行 → 解密 → 用新加密 key 重加密 → 重算 MAC(新 verify key) → 写回
```

不能先换 MAC 再换密文，也不能只换其中一个；否则验签会因载荷变化失败。

---

## 5. 失败模式与安全规则

| 场景 | 行为 |
|---|---|
| `ENC:`/`MAC:` 的 key id 不在密钥环 | 返回明确错误，**不尝试其它 key** |
| 未知 alg | 返回错误，不降级 |
| key id 重复 | `Initialize` 失败 |
| active key 不存在/为空 | `Initialize` 失败 |
| 头部被篡改 | AEAD AAD / MAC 绑定 key id 后验证失败 |
| 旧格式（无 magic） | 用 legacy `SecretBy`/`VerifyBy` 读取 |
| 旧格式解密失败 | 原错误返回，不自动尝试新 key |
| hook 与内置密钥环同时配置 | `Initialize` 报错，避免两套密钥来源 |

---

## 6. 兼容与发布顺序

### Phase 1：只加 keyring，不改格式

- 增加配置字段与校验；
- `SecretKeys` 为空时行为完全不变；
- 旧数据、旧写入不受影响；
- 这一步可以先发布，不动任何密文。

### Phase 2：读双格式，写 v2

- 新写入带 key id；
- 读取同时支持旧 `ENC:`/`MAC:` 和 v2；
- 旧数据无需立即迁移；
- 观察 unknown key id、解密失败。

### Phase 3：迁移数据

- 应用批量重加密/重签；
- 确认旧 key 使用量为 0；
- 再进入 Phase 4。

### Phase 4：移除旧 key / 旧格式读

- 删除 legacy 分支；
- 删除 `SecretBy`/`VerifyBy` 兼容字段（或保留 deprecated）；
- 这是破坏性变更，需要 CHANGELOG。

> 当前 `VerifyMacVer` 已删除，本方案从“单一 MAC 格式”起步，不需要再处理 MAC 版本开关。

---

## 7. 需要拍板的决策点

| # | 决策 | 选项 | 建议 |
|---|---|---|---|
| 1 | key id 放在值内还是新列 | 值内 envelope / 增加 `enc_key_id`、`mac_key_id` 列 | **值内**；不改表结构 |
| 2 | 是否保留旧 `ENC:`/`MAC:` 读取 | 保留一个迁移窗口 / 直接只支持新格式 | **保留 legacy 读取**，迁移完再删 |
| 3 | 新格式前缀 | 新 `ENC2:`/`MAC2:` / 保持 `ENC:`/`MAC:` + magic | **保持标签 + magic**；避免列格式/查询条件变化 |
| 4 | 密钥来源 | 引擎密钥环 / 仅应用 hook | **引擎密钥环 + 可选 hook**；两者不混用 |
| 5 | active key 选择 | 显式 `Active*KeyID` / slice 第一个 | **显式 ID**；避免顺序依赖 |
| 6 | 迁移驱动 | 引擎内置后台任务 / 应用批量调用 | **应用批量调用**；引擎不引入后台 goroutine |
| 7 | 旧 key 退休 | 自动按时间 / 显式移除 | **显式移除**；`RetireAt` 只做提示 |
| 8 | 算法标识 | 只做 AES-GCM/HMAC-SHA256 / 预留 alg 字节 | **预留 alg 字节**，当前只实现两种 |

---

## 8. 测试清单

实现后至少覆盖：

1. v2 加密/解密往返；key id 正确写入；
2. 旧 `ENC:` 仍可读；新写入用 active key；
3. 未知 key id 解密失败且不降级；
4. 头部（key id/alg/iter）被篡改时 AAD 认证失败；
5. 同一明文两次加密 nonce/salt 不同；
6. 缓存键包含 key id；切换 active key 后旧缓存不串用；
7. v2 MAC 写入/验证；key id 被替换时 HMAC 失败；
8. 旧 `MAC:` 读取；新写入 active verify key；
9. `VerifySignRow` 使用 active key；
10. 加密 + 验签同时轮换的顺序测试；
11. key 重复/active 缺失/空密钥/非法 iter 的 `Initialize` 失败；
12. hook 与 keyring 混用报错；自定义 hook 收到正确 key id；
13. 批量迁移：旧 key 行数降为 0 后再移除 key，旧数据读取失败符合预期。

---

## 9. 工作量估计

| 阶段 | 内容 | 估计 |
|---|---|---|
| Phase 1 | 配置结构、校验、兼容字段 | 0.5–1 天 |
| Phase 2 | ENC/MAC envelope 编解码、双格式读、缓存键 | 2–3 天 |
| Phase 3 | 迁移辅助/示例、错误处理、文档 | 1–2 天 |
| Phase 4 | 删除 legacy、CHANGELOG、回归 | 0.5–1 天 |
| 合计 | 含测试 | **4–7 天** |

---

## 10. 结论

推荐采用：

> **值内 key id + 多密钥环 + `ENC:`/`MAC:` 标签内 magic 版本头 + 旧格式只读迁移窗口。**

它满足在线轮换、灰度、回滚和算法敏捷，同时不改表结构、不重新引入全局版本开关。  
需要你拍板的主要是第 7 节 8 个决策点；默认按“建议”列执行即可。
