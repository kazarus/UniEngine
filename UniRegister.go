// UniRegister
// #注册表:类/表/字段/主键登记与 SQL 预生成

package UniEngine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// invalidTable 构造一个**未注册**的表载体,仅携带 LastError。
// #沿用 RegisterClass 既有的链式约定:调用方(如 SetKeys/SetSecret)见到 LastError 即短路,
// #签名保持 *TUniTable 不变,非法输入既不进注册表也不 panic。
func invalidTable(TableName string, eror error) *TUniTable {
	return &TUniTable{TableName: TableName, LastError: eror}
}

// cloneTableMetadata 在引擎写锁内复制已发布的表。查询/写入取得旧指针后可继续
// 只读访问；RegisterTable/Field/Pkeys 只修改副本，再一次性发布到所有别名。
func cloneTableMetadata(Old *TUniTable) *TUniTable {
	Copy := *Old
	Copy.HashField = make(map[string]TUniField, len(Old.HashField))
	for Key, Field := range Old.HashField {
		Copy.HashField[Key] = Field
	}
	Copy.HashPkeys = make(map[string]TUniField, len(Old.HashPkeys))
	for Key, Field := range Old.HashPkeys {
		Copy.HashPkeys[Key] = Field
	}
	Copy.ListField = append([]TUniField(nil), Old.ListField...)
	Copy.ListPkeys = append([]TUniField(nil), Old.ListPkeys...)
	Copy.ColSecret = append([]TUniField(nil), Old.ColSecret...)
	Copy.ColVerify = append([]TUniField(nil), Old.ColVerify...)
	return &Copy
}

// publishTableMetadata 保持表名/类名双键指向同一快照;调用方已持引擎写锁。
func (this *TUniEngine) publishTableMetadata(Old, Next *TUniTable) {
	if Old != nil {
		for Key, Table := range this.HashTabl {
			if Table == Old {
				this.HashTabl[Key] = Next
			}
		}
	}
	this.HashTabl[strings.ToLower(Next.TableName)] = Next
}

// identGuard 注册类 API 的统一标识符白名单校验(防注入;与 Write/Exist* 路径同一 validIdent)。
// #运行时传入的表名/约束名一律经此校验——这些字符串会被直接拼进生成的 SQL 文本。
// #Sentinel 区分语义(表名用 ErrInvalidTableName,字段名用 ErrInvalidFieldName),便于调用方 errors.Is 判别。
func identGuard(Sentinel error, What string, Name string) error {
	if !validIdent(Name) {
		return fmt.Errorf("%w: %s [%s] (only letters/digits/underscore/dot are allowed); check your code;", Sentinel, What, Name)
	}
	return nil
}

// columnGuard 列名专用白名单:在 identGuard 基础上禁止点号。
// 表名允许 schema.table,列名不允许——"a.b" 会被 quoteIdent 引成 "a"."b",
// 在 PG/Oracle 下静默指向另一 schema 的同名列,而非报错。
func columnGuard(Sentinel error, What string, Name string) error {
	if !validColumnName(Name) {
		return fmt.Errorf("%w: %s [%s] (only letters/digits/underscore are allowed for column names); check your code;", Sentinel, What, Name)
	}
	return nil
}

func (this *TUniEngine) RegisterClass(Class interface{}, TableName string) *TUniTable {
	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	//#类型守卫:裸 nil/非结构体(含指针)返回带 LastError 的未注册表(链式约定),不再反射 panic
	if Class == nil {
		return invalidTable(TableName, fmt.Errorf("%w: RegisterClass got a nil param; needs a struct value; check your code;", ErrNilParam))
	}

	t := reflect.TypeOf(Class)
	if t.Kind() != reflect.Struct {
		return invalidTable(TableName, fmt.Errorf("UniEngine: RegisterClass needs a struct value, but got kind [%s]; check your code;", t.Kind()))
	}

	//#表名白名单:RegisterClass 的表名会经 PrepareRunSQL 直接拼进 SQL(CRUD 表名不引号),
	//#此前只在写路径(resolveTarget)校验,PrepareRunSQL 路径构成注入面
	if eror := identGuard(ErrInvalidTableName, "RegisterClass table name", TableName); eror != nil {
		return invalidTable(TableName, eror)
	}

	n := t.NumField()

	//#已见列名(小写)→首个 Go 字段名:同一列名映射多个字段时,HashField 取最后、
	//#orderedField 取先,写入与读取会填到不同字段(评审 Q7),登记期直接拒绝
	SeenCols := make(map[string]string, n)

	var UniTable = &TUniTable{}
	UniTable.HashField = make(map[string]TUniField, 0)
	UniTable.HashPkeys = make(map[string]TUniField, 0)
	UniTable.TableName = TableName
	UniTable.ClassType = t

	for i := 0; i < n; i++ {
		f := t.Field(i)

		// 跳过未导出字段:反射无法读写(FieldByName().Interface() 会 panic)
		if f.PkgPath != "" {
			continue
		}

		var UniField = TUniField{}
		UniField.AttriName = f.Name

		UniField.initialize(f.Tag.Get(this.ColLabel))

		// 跳过无 db tag 的字段:FieldName 为空会以 "" 键污染注册表并生成非法 SQL
		if UniField.FieldName == "" {
			continue
		}

		//#"db:\"-\" / db:\"_\"" 是只读标记,不是列名,豁免校验(多个标记字段共存合法);其余列名走白名单
		if UniField.FieldName != "-" && UniField.FieldName != "_" {
			if eror := columnGuard(ErrInvalidFieldName, fmt.Sprintf("RegisterClass field tag on %s", f.Name), UniField.FieldName); eror != nil {
				return invalidTable(TableName, eror)
			}

			LowerTxt := strings.ToLower(UniField.FieldName)
			if PrevAttri, Dup := SeenCols[LowerTxt]; Dup {
				return invalidTable(TableName, fmt.Errorf("UniEngine: RegisterClass duplicate column [%s] on table [%s]: fields [%s] and [%s]; each column must map to exactly one field", UniField.FieldName, TableName, PrevAttri, f.Name))
			}
			SeenCols[LowerTxt] = f.Name
		}

		UniTable.HashField[strings.ToLower(UniField.FieldName)] = UniField
		// 保留 struct 声明顺序，供 INSERT/UPDATE 列序生成
		UniTable.ListField = append(UniTable.ListField, UniField)

		//#tag 标记的加密列(db:"...,secreton")同步置位表级 SecretOn 并登记加密列清单,与 SetSecret 路径一致
		if UniField.SecretOn {
			UniTable.SecretOn = true
			UniTable.ColSecret = append(UniTable.ColSecret, UniField)
		}
	}

	// 同时以小写表名（PrepareTables/PrepareRunSQL 路径）与类全名（SaveIt/Insert/Delete/Select* 路径）注册，
	// 统一两套 Key 的可见性；两者指向同一表
	this.HashTabl[strings.ToLower(TableName)] = UniTable
	this.HashTabl[t.String()] = UniTable

	return UniTable
}

func (this *TUniEngine) RegisterTable(TableName string, IPriority int64) *TUniTable {
	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	if eror := identGuard(ErrInvalidTableName, "RegisterTable table name", TableName); eror != nil {
		return invalidTable(TableName, eror)
	}

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	Old := UniTable
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	} else {
		UniTable = cloneTableMetadata(UniTable)
	}
	UniTable.IPriority = IPriority

	this.publishTableMetadata(Old, UniTable)

	return UniTable
}

func (this *TUniEngine) RegisterField(TableName string, FieldName string) *TUniTable {
	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	if eror := identGuard(ErrInvalidTableName, "RegisterField table name", TableName); eror != nil {
		return invalidTable(TableName, eror)
	}
	if eror := columnGuard(ErrInvalidFieldName, "RegisterField field name", FieldName); eror != nil {
		return invalidTable(TableName, eror)
	}

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	Old := UniTable
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	} else {
		UniTable = cloneTableMetadata(UniTable)
	}

	var UniField = TUniField{}
	UniField.AttriName = ""
	UniField.FieldName = strings.ToLower(FieldName)
	UniField.TableName = strings.ToLower(TableName)

	LowerField := strings.ToLower(UniField.FieldName)
	if Existing, Ok := UniTable.HashField[LowerField]; Ok && Existing.AttriName != "" {
		UniField = Existing //#保留 RegisterClass 的反射映射,不以手工空映射覆盖
	}
	UniTable.HashField[LowerField] = UniField

	this.publishTableMetadata(Old, UniTable)

	return UniTable
}

func (this *TUniEngine) RegisterPkeys(TableName string, FieldName string) *TUniTable {
	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	if eror := identGuard(ErrInvalidTableName, "RegisterPkeys table name", TableName); eror != nil {
		return invalidTable(TableName, eror)
	}
	if eror := columnGuard(ErrInvalidFieldName, "RegisterPkeys field name", FieldName); eror != nil {
		return invalidTable(TableName, eror)
	}

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		//#表未登记时不再返回 nil(旧实现会让 .SetKeys() 等链式调用 nil 解引用 panic),
		//#改为返回带 LastError 的未注册表(与 RegisterClass 守卫同约定)
		return invalidTable(TableName, fmt.Errorf("%w: RegisterPkeys table [%s] is not registered; call RegisterTable/RegisterClass first;", ErrUnregisteredClass, TableName))
	}
	//#同 SetKeys/AutoKeys:SetVerify 之后主键冻结。此处直接返回未注册表(不克隆、不发布),
	//#保证已发布快照的主键集合不变(评审 Q2 顺序变体)
	if UniTable.VerifyOn {
		return invalidTable(TableName, fmt.Errorf("%w: table[%s] RegisterPkeys is not allowed after SetVerify; register pkeys before SetVerify (re-register the class to change pkeys)", ErrPkeysFrozenAfterVerify, TableName))
	}

	Old := UniTable
	UniTable = cloneTableMetadata(UniTable)

	{
		var UniField = TUniField{}
		UniField.AttriName = ""
		UniField.FieldName = strings.ToLower(FieldName)
		UniField.TableName = strings.ToLower(TableName)

		LowerField := strings.ToLower(UniField.FieldName)
		if Existing, Ok := UniTable.HashField[LowerField]; Ok && Existing.AttriName != "" {
			UniField = Existing //#保留类字段映射,避免后续 Update/Delete 反射定位失败
		}
		UniTable.HashPkeys[LowerField] = UniField

		this.publishTableMetadata(Old, UniTable)
	}

	return UniTable
}

func (this *TUniEngine) GetTable(TableName string) *TUniTable {
	return this.tableByName(TableName)
}

// #ListSecretTable 返回全部含加密列的注册表(读锁,按表名去重排序)
// #HashTabl 同时以表名与类全名双键登记且指向同一表,须按 TableName 去重;
// #供存量加解密迁移等注册表驱动场景遍历,新增 SetSecret/secreton 登记即自动纳入
func (this *TUniEngine) ListSecretTable() []*TUniTable {
	this.rlockTables()
	defer this.mu.RUnlock()

	var HashSeen = make(map[string]bool, len(this.HashTabl))
	var ListTable = make([]*TUniTable, 0, 4)

	for _, UniTable := range this.HashTabl {
		if UniTable == nil || !UniTable.SecretOn {
			continue
		}
		if HashSeen[UniTable.TableName] {
			continue
		}
		HashSeen[UniTable.TableName] = true
		ListTable = append(ListTable, UniTable)
	}

	sort.Slice(ListTable, func(i, j int) bool { return ListTable[i].TableName < ListTable[j].TableName })

	return ListTable
}

func (this *TUniEngine) PrepareTables(TableName string) error {
	this.lockTables()
	defer this.mu.Unlock()

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		//#未注册的表静默跳过,保持旧行为
		return nil
	}

	//#按确定序重建(ListField 既有序优先,其余按字段名排序),避免 map 迭代序导致 SQL 文本漂移
	ListField := make([]TUniField, 0, len(UniTable.HashField))
	for _, ItemPara := range UniTable.orderedField() {
		if ItemPara.ReadOnly {
			continue
		}
		ListField = append(ListField, ItemPara)
	}
	UniTable.ListField = ListField

	ListPkeys := make([]TUniField, 0, len(UniTable.HashPkeys))
	for _, ItemPara := range UniTable.orderedPkeys() {
		if ItemPara.ReadOnly {
			continue
		}
		ListPkeys = append(ListPkeys, ItemPara)
	}
	UniTable.ListPkeys = ListPkeys

	return nil
}

func (this *TUniEngine) PrepareRunSQL(TableName string, QueryType TQueryType) (string, []TUniField, []TUniField, error) {
	this.lockTables()
	defer this.mu.Unlock()

	var SqlResult string
	var ListField = make([]TUniField, 0)

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		//#未注册的表:返回空结果(旧代码在此对 nil 指针取 ListPkeys 会 panic)
		return SqlResult, ListField, nil, nil
	}

	switch QueryType {
	case EtSelect:
		{
			var SqlWhere []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedPkeys() {
				//#只跳过"只读标记字段"(db:"-"/db:"_" 没有真实列名);
				//#db:"created_at,readonly" 这类**有真实列名的只读列**必须纳入定位键,
				//#否则本函数产出的 WHERE 会漏掉该主键,与 Delete/Update/SaveIt 的 pkeyFields 口径不一致
				if !isPkeyUsable(ItemPara) {
					continue
				}
				SqlWhere = append(SqlWhere, fmt.Sprintf("    and %s=%s", this.getColParam(ItemPara.FieldName), this.getValParam(ColIndex)))
				ColIndex = ColIndex + 1
			}

			if len(UniTable.HashPkeys) > 0 {
				SqlResult = fmt.Sprintf("where 1=1 %s", strings.Join(SqlWhere, ""))
				UniTable.SqlSelect = SqlResult
			}
		}
	case EtInsert:
		{
			var ColList []string
			var ValList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedField() {
				if ItemPara.ReadOnly {
					continue
				}
				ColList = append(ColList, this.getColParam(ItemPara.FieldName))
				ValList = append(ValList, this.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			if len(ColList) > 0 {
				SqlResult = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", UniTable.TableName, strings.Join(ColList, ","), strings.Join(ValList, ","))
				UniTable.SqlInsert = SqlResult
			}
		}
	case EtUpdate:
		{
			var SetList []string
			var KeyList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedField() {
				if ItemPara.ReadOnly {
					continue
				}

				if _, Valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; Valid {
					ItemPara.PkeyOnly = true
					ListField = append(ListField, ItemPara)
					continue
				}

				SetList = append(SetList, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			for _, ItemPara := range UniTable.orderedPkeys() {
				KeyList = append(KeyList, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
				ColIndex = ColIndex + 1
			}

			if len(SetList) == 0 {
				return "", ListField, UniTable.ListPkeys, fmt.Errorf("UniEngine: no updatable column for table:%s", UniTable.TableName)
			}

			SqlResult = fmt.Sprintf("update %s set %s where 1=1 and %s", UniTable.TableName, strings.Join(SetList, ","), strings.Join(KeyList, " and "))
			UniTable.SqlUpdate = SqlResult
		}
	case EtDelete:
		{
			var KeyList []string
			ColIndex := 1

			for _, ItemPara := range UniTable.orderedPkeys() {
				KeyList = append(KeyList, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
				ColIndex = ColIndex + 1
			}

			if len(KeyList) == 0 {
				return "", ListField, UniTable.ListPkeys, fmt.Errorf("UniEngine: no deleteable column for table:%s", UniTable.TableName)
			}

			SqlResult = fmt.Sprintf("delete from %s where 1=1 and %s", UniTable.TableName, strings.Join(KeyList, " and "))
			UniTable.SqlDelete = SqlResult
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}
