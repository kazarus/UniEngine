// UniRegister
// #注册表:类/表/字段/主键登记与 SQL 预生成
package UniEngine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func (this *TUniEngine) RegisterClass(Class interface{}, TableName string) *TUniTable {

	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	t := reflect.TypeOf(Class)
	n := t.NumField()

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
	// 统一两套 key 的可见性；两者指向同一表
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

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	}
	UniTable.IPriority = IPriority

	this.HashTabl[strings.ToLower(TableName)] = UniTable

	return UniTable
}

func (this *TUniEngine) RegisterField(TableName string, FieldName string) *TUniTable {

	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	}

	var UniField = TUniField{}
	UniField.AttriName = ""
	UniField.FieldName = strings.ToLower(FieldName)
	UniField.TableName = strings.ToLower(TableName)

	UniTable.HashField[strings.ToLower(UniField.FieldName)] = UniField

	this.HashTabl[strings.ToLower(TableName)] = UniTable

	return UniTable
}

func (this *TUniEngine) RegisterPkeys(TableName string, FieldName string) *TUniTable {

	this.lockTables()
	defer this.mu.Unlock()

	if this.HashTabl == nil {
		this.HashTabl = make(map[string]*TUniTable, 0)
	}

	UniTable, Valid := this.HashTabl[strings.ToLower(TableName)]
	if Valid {
		var UniField = TUniField{}
		UniField.AttriName = ""
		UniField.FieldName = strings.ToLower(FieldName)
		UniField.TableName = strings.ToLower(TableName)

		UniTable.HashPkeys[strings.ToLower(UniField.FieldName)] = UniField

		this.HashTabl[strings.ToLower(TableName)] = UniTable
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
	for _, ItemPara := range UniTable.orderedFields() {
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
				if ItemPara.ReadOnly {
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
			var colList []string
			var paramList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedFields() {
				if ItemPara.ReadOnly {
					continue
				}
				colList = append(colList, this.getColParam(ItemPara.FieldName))
				paramList = append(paramList, this.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			if len(colList) > 0 {
				SqlResult = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", UniTable.TableName, strings.Join(colList, ","), strings.Join(paramList, ","))
				UniTable.SqlInsert = SqlResult
			}
		}
	case EtUpdate:
		{
			var setList []string
			var keyList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedFields() {
				if ItemPara.ReadOnly {
					continue
				}

				if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
					ItemPara.PkeyOnly = true
					ListField = append(ListField, ItemPara)
					continue
				}

				setList = append(setList, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			for _, ItemPara := range UniTable.orderedPkeys() {
				keyList = append(keyList, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
				ColIndex = ColIndex + 1
			}

			if len(setList) == 0 {
				return "", ListField, UniTable.ListPkeys, fmt.Errorf("UniEngine: no updatable column for table:%s", UniTable.TableName)
			}

			SqlResult = fmt.Sprintf("update %s set %s where 1=1 and %s", UniTable.TableName, strings.Join(setList, ","), strings.Join(keyList, " and "))
			UniTable.SqlUpdate = SqlResult
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}

// ---------------------------------------------------------------------------
// 查询公共核心
// ---------------------------------------------------------------------------

// selectColsCtx 标量查询公共核心：执行并取最后一行的单列值（SelectD/F/S 共用）。
