// UniStatus
// #完整性状态事件(TUniStatus)、通知钩子(TStatusHook)与引擎侧派发。
// #事件类型与验签实现解耦:验签路径只负责填充定位信息,派发/钩子生命周期由本文件维护,
// #后续其它状态来源可复用同一事件通道扩展字段。

package UniEngine

// #TUniPkey 行主键实参:Column 为登记列名(小写),Value 为该字段的原始 Go 值。
// #供下游审计定位(写审计表/追查具体行);JSON 标签便于直接序列化落库。
type TUniPkey struct {
	Column string      `json:"column"`
	Value  interface{} `json:"value"`
}

// #完整性状态事件:交给 TStatusHook 的载荷(当前由验签失败触发)。
// #携带定位信息、引擎实例与当前行主键,供应用写日志/监控或直接落审计表;
// #不含主键之外的行值、MAC 与 HMAC 载荷。Pkeys 含主键实参,写日志时请注意脱敏。
type TUniStatus struct {
	Engine   *TUniEngine //#触发事件的引擎实例(多引擎共用钩子/审计落库时用于取 Db/Provider/DataBase)
	TablFrom string      //#来源表名(注册表名,SetVerify 所在表)
	ModuleBy string      //#表所属模块(UniTable.ModuleBy 快照);未登记时为空串
	MethodIn string      //#触发入口方法(输入):Select/SelectL/SelectM/SelectH(*Ctx 变体同名)
	Mode     int64       //#本次生效的 VerificationMode;0/1=宽容,2=严格
	Row      int         //#结果集行号(0 基);-1 表示非行级失败(列投影不足,尚未读到行)
	Pkeys    []TUniPkey  //#当前行主键实参(登记顺序,仅限结果集实际投影的主键列);Row=-1 或未投影时为空
	Accepted bool        //#true=宽容模式已放行(行仍返回调用方);false=失败已作为 error 上抛
	ErrorOut error       //#失败原因(输出);恒非 nil,文本不含行值/载荷
}

// #应用完整性状态钩子:验签失败时同步回调一次——严格模式报错、宽容模式放行、
// #严格模式列投影不足都在此列。钩子只观察、不参与判定:返回值与错误文本不受影响;
// #实现须并发安全且不得阻塞查询(引擎在查询 goroutine 内同步调用,不另起协程);
// #panic 与其它应用钩子(SecretHook/VerifyHook)一样沿调用栈向上传播。
type TStatusHook func(Status TUniStatus)

// #emitStatus 把事件交给应用钩子;未设置钩子时直接返回。
// #引擎侧只补齐 Engine(事件来源),其余字段由调用方填充,避免事件类型与具体校验路径耦合。
// #在调用方 goroutine 内同步执行,不另起协程。
func (this *TUniEngine) emitStatus(Status TUniStatus) {
	Hook := this.StatusHook
	if Hook == nil {
		return
	}

	Status.Engine = this

	Hook(Status)
}
