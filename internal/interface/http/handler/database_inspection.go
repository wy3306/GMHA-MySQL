package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	taskdomain "gmha/internal/domain/task"
)

type databaseInspectionCheck struct {
	TaskID         string `json:"task_id"`
	Cluster        string `json:"cluster,omitempty"`
	Machine        string `json:"machine,omitempty"`
	IP             string `json:"ip,omitempty"`
	Port           int    `json:"port,omitempty"`
	Level          string `json:"level"`
	Category       string `json:"category"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
	Value          string `json:"value,omitempty"`
	Threshold      string `json:"threshold,omitempty"`
	Description    string `json:"description,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

type databaseInspectionTarget struct {
	TaskID      string    `json:"task_id"`
	Cluster     string    `json:"cluster,omitempty"`
	MachineID   string    `json:"machine_id"`
	Machine     string    `json:"machine"`
	IP          string    `json:"ip"`
	Port        int       `json:"port"`
	Hostname    string    `json:"hostname,omitempty"`
	Version     string    `json:"version,omitempty"`
	Level       string    `json:"level"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	Score       int       `json:"score"`
	Passed      int       `json:"passed"`
	Warnings    int       `json:"warnings"`
	Critical    int       `json:"critical"`
	Information int       `json:"information"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

type databaseInspectionResult struct {
	Ready    bool                       `json:"ready"`
	Pending  int                        `json:"pending"`
	Failed   int                        `json:"failed"`
	Targets  []databaseInspectionTarget `json:"targets"`
	Checks   []databaseInspectionCheck  `json:"checks"`
	Exported time.Time                  `json:"exported_at"`
}

func databaseInspectionCommand(client string, deep bool) string {
	level := "standard"
	if deep {
		level = "deep"
	}
	statements := []string{
		"SET SESSION group_concat_max_len=1048576",
		"SELECT CONCAT('GMHA_INSPECTION_META\\t',@@hostname,'\\t',@@version,'\\t',@@port,'\\t',UTC_TIMESTAMP())",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t可用性\\tavailability\\t数据库连接\\tcritical\\tpass\\t在线\\t可连接\\t数据库可正常建立管理连接\\t保持连接监控与告警')",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t可用性\\tuptime\\t实例运行时长\\twarning\\t',IF(VARIABLE_VALUE+0<3600,'warning','pass'),'\\t',VARIABLE_VALUE,' 秒\\t>= 3600 秒\\t',IF(VARIABLE_VALUE+0<3600,'实例近期发生过重启','实例持续稳定运行'),'\\t',IF(VARIABLE_VALUE+0<3600,'确认重启原因并检查错误日志','持续关注计划外重启')) FROM performance_schema.global_status WHERE VARIABLE_NAME='Uptime'",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t连接\\tconnection_usage\\t连接使用率\\tcritical\\t',CASE WHEN c.v/m.v>=0.9 THEN 'critical' WHEN c.v/m.v>=0.7 THEN 'warning' ELSE 'pass' END,'\\t',ROUND(c.v/m.v*100,2),'%\\t< 70%\\t当前连接 ',c.v,' / 最大连接 ',m.v,'\\t',CASE WHEN c.v/m.v>=0.9 THEN '立即排查连接泄漏并评估扩容' WHEN c.v/m.v>=0.7 THEN '检查连接池配置和空闲连接' ELSE '保持当前连接池策略' END) FROM (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') c JOIN (SELECT @@global.max_connections+0 v) m",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t负载\\trunning_threads\\t活跃线程\\twarning\\t',CASE WHEN VARIABLE_VALUE+0>=64 THEN 'critical' WHEN VARIABLE_VALUE+0>=32 THEN 'warning' ELSE 'pass' END,'\\t',VARIABLE_VALUE,'\\t< 32\\t并发执行线程数量\\t',IF(VARIABLE_VALUE+0>=32,'检查高并发 SQL、锁等待和 CPU 饱和度','保持性能趋势监控')) FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_running'",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\tSQL质量\\tslow_query_ratio\\t慢查询比例\\twarning\\t',CASE WHEN s.v/GREATEST(q.v,1)>=0.01 THEN 'critical' WHEN s.v/GREATEST(q.v,1)>=0.001 THEN 'warning' ELSE 'pass' END,'\\t',ROUND(s.v/GREATEST(q.v,1)*100,4),'%\\t< 0.1%\\t累计慢查询 ',s.v,' / Questions ',q.v,'\\t',IF(s.v/GREATEST(q.v,1)>=0.001,'结合慢日志和执行计划治理高频慢 SQL','持续采集慢查询趋势')) FROM (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Slow_queries') s JOIN (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Questions') q",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t连接\\taborted_connects\\t失败连接比例\\twarning\\t',CASE WHEN a.v/GREATEST(c.v,1)>=0.01 THEN 'warning' ELSE 'pass' END,'\\t',ROUND(a.v/GREATEST(c.v,1)*100,4),'%\\t< 1%\\t失败连接 ',a.v,' / 总连接 ',c.v,'\\t',IF(a.v/GREATEST(c.v,1)>=0.01,'检查账号密码、网络抖动和连接超时','保持连接失败监控')) FROM (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Aborted_connects') a JOIN (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Connections') c",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t日志与复制\\tbinlog_format\\tBinlog 格式\\twarning\\t',IF(@@global.binlog_format='ROW','pass','warning'),'\\t',@@global.binlog_format,'\\tROW\\t当前 Binlog 格式\\t',IF(@@global.binlog_format='ROW','保持 ROW 格式','建议评估切换为 ROW 以提高复制一致性'))",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t持久性\\tdurability\\t事务持久性参数\\tcritical\\t',CASE WHEN @@global.sync_binlog=1 AND @@global.innodb_flush_log_at_trx_commit=1 THEN 'pass' WHEN @@global.sync_binlog=0 OR @@global.innodb_flush_log_at_trx_commit=0 THEN 'critical' ELSE 'warning' END,'\\t','sync_binlog=',@@global.sync_binlog,', flush=',@@global.innodb_flush_log_at_trx_commit,'\\tsync_binlog=1, flush=1\\tBinlog 与 Redo 刷盘策略\\t',IF(@@global.sync_binlog=1 AND @@global.innodb_flush_log_at_trx_commit=1,'保持强持久性配置','评估数据丢失风险并在维护窗口调整'))",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t高可用\\tgtid_mode\\tGTID 模式\\twarning\\t',IF(@@global.gtid_mode='ON','pass','warning'),'\\t',@@global.gtid_mode,'\\tON\\t全局事务标识模式\\t',IF(@@global.gtid_mode='ON','保持 GTID 一致性检查','高可用集群建议规划启用 GTID'))",
		"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t容量\\tdatabase_size\\t业务数据容量\\tinfo\\tinfo\\t',ROUND(COALESCE(SUM(DATA_LENGTH+INDEX_LENGTH),0)/1024/1024/1024,2),' GB\\t信息项\\t非系统库数据与索引总量\\t结合增长趋势规划磁盘容量') FROM information_schema.tables WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
	}
	if deep {
		statements = append(statements,
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t表结构\\ttables_without_pk\\t无主键表\\tcritical\\t',CASE WHEN COUNT(*)=0 THEN 'pass' WHEN COUNT(*)<=3 THEN 'warning' ELSE 'critical' END,'\\t',COUNT(*),' 张\\t0 张\\t业务库中没有主键的 InnoDB 表\\t为无主键表设计稳定主键，降低复制与行定位风险') FROM information_schema.tables t WHERE t.TABLE_TYPE='BASE TABLE' AND t.ENGINE='InnoDB' AND t.TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') AND NOT EXISTS (SELECT 1 FROM information_schema.statistics s WHERE s.TABLE_SCHEMA=t.TABLE_SCHEMA AND s.TABLE_NAME=t.TABLE_NAME AND s.INDEX_NAME='PRIMARY')",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t表结构\\tnon_innodb_tables\\t非 InnoDB 表\\twarning\\t',IF(COUNT(*)=0,'pass','warning'),'\\t',COUNT(*),' 张\\t0 张\\t业务库中的非 InnoDB 基础表\\t确认引擎需求，优先迁移到 InnoDB') FROM information_schema.tables WHERE TABLE_TYPE='BASE TABLE' AND COALESCE(ENGINE,'')<>'InnoDB' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t空间\\tfragmented_tables\\t高碎片表\\twarning\\t',CASE WHEN COUNT(*)=0 THEN 'pass' WHEN COUNT(*)<=5 THEN 'warning' ELSE 'critical' END,'\\t',COUNT(*),' 张\\t0 张\\tDATA_FREE 超过 1GB 且超过表数据 30% 的表\\t评估 OPTIMIZE TABLE 或在线重建并预留磁盘空间') FROM information_schema.tables WHERE ENGINE='InnoDB' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') AND DATA_FREE>1073741824 AND DATA_FREE>DATA_LENGTH*0.3",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t事务\\tlong_transactions\\t长事务\\tcritical\\t',CASE WHEN COUNT(*)=0 THEN 'pass' WHEN COUNT(*)<=2 THEN 'warning' ELSE 'critical' END,'\\t',COUNT(*),' 个\\t0 个\\t持续超过 300 秒的活动事务\\t定位会话与业务调用链，尽快提交或回滚长事务') FROM information_schema.innodb_trx WHERE TIMESTAMPDIFF(SECOND,trx_started,NOW())>300",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t内存与临时表\\tdisk_tmp_ratio\\t磁盘临时表比例\\twarning\\t',CASE WHEN d.v/GREATEST(t.v,1)>=0.25 THEN 'critical' WHEN d.v/GREATEST(t.v,1)>=0.1 THEN 'warning' ELSE 'pass' END,'\\t',ROUND(d.v/GREATEST(t.v,1)*100,2),'%\\t< 10%\\t磁盘临时表 ',d.v,' / 全部临时表 ',t.v,'\\t',IF(d.v/GREATEST(t.v,1)>=0.1,'优化排序分组 SQL，并评估 tmp_table_size','保持临时表趋势监控')) FROM (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_disk_tables') d JOIN (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_tables') t",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\tInnoDB\\tbuffer_pool_hit\\tBuffer Pool 命中率\\twarning\\t',CASE WHEN 1-r.v/GREATEST(q.v,1)<0.95 THEN 'critical' WHEN 1-r.v/GREATEST(q.v,1)<0.99 THEN 'warning' ELSE 'pass' END,'\\t',ROUND((1-r.v/GREATEST(q.v,1))*100,4),'%\\t>= 99%\\t基于逻辑读和物理读估算\\t',IF(1-r.v/GREATEST(q.v,1)<0.99,'检查热点数据集与 Buffer Pool 容量','保持内存命中率趋势监控')) FROM (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_reads') r JOIN (SELECT VARIABLE_VALUE+0 v FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_read_requests') q",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\tInnoDB\\thistory_list\\tUndo 历史链长度\\twarning\\t',CASE WHEN COALESCE(MAX(COUNT),0)>=100000 THEN 'critical' WHEN COALESCE(MAX(COUNT),0)>=10000 THEN 'warning' ELSE 'pass' END,'\\t',COALESCE(MAX(COUNT),0),'\\t< 10000\\tPurge 尚未清理的历史版本数量\\t',IF(COALESCE(MAX(COUNT),0)>=10000,'排查长事务并确认 Purge 线程工作状态','保持长事务与 Purge 监控')) FROM information_schema.innodb_metrics WHERE NAME='trx_rseg_history_len'",
			"SELECT CONCAT('GMHA_INSPECTION_CHECK\\t容量\\tlarge_tables\\t超大表数量\\tinfo\\tinfo\\t',COUNT(*),' 张\\t信息项\\t数据与索引超过 100GB 的表\\t为超大表规划归档、分区或拆分策略') FROM information_schema.tables WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') AND DATA_LENGTH+INDEX_LENGTH>=107374182400",
		)
	}
	statements = append(statements, "SELECT CONCAT('GMHA_INSPECTION_END\\t"+level+"\\t',UTC_TIMESTAMP())")
	return client + " --execute=" + shellQuote(strings.Join(statements, "; ")+";")
}

func (h *TaskHandler) HandleDatabaseInspectionResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := h.loadDatabaseInspection(r, inspectionTaskIDs(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *TaskHandler) HandleDatabaseInspectionReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := h.loadDatabaseInspection(r, inspectionTaskIDs(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !result.Ready {
		writeError(w, http.StatusConflict, errors.New("inspection is still running"))
		return
	}
	contents, err := buildInspectionDOCX(result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name := "gmha-database-inspection-" + time.Now().UTC().Format("20060102-150405") + ".docx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	_, _ = w.Write(contents)
}

func (h *TaskHandler) HandleDatabaseInspectionData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := h.loadDatabaseInspection(r, inspectionTaskIDs(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !result.Ready {
		writeError(w, http.StatusConflict, errors.New("inspection is still running"))
		return
	}
	contents, err := buildInspectionXLSX(result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name := "gmha-database-inspection-data-" + time.Now().UTC().Format("20060102-150405") + ".xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	_, _ = w.Write(contents)
}

func inspectionTaskIDs(r *http.Request) []string {
	raw := strings.Split(strings.TrimSpace(r.URL.Query().Get("task_ids")), ",")
	result := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, id := range raw {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

func (h *TaskHandler) loadDatabaseInspection(r *http.Request, ids []string) (databaseInspectionResult, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return databaseInspectionResult{}, errors.New("between 1 and 1000 inspection task_ids are required")
	}
	result := databaseInspectionResult{Ready: true, Targets: make([]databaseInspectionTarget, 0, len(ids)), Checks: []databaseInspectionCheck{}, Exported: time.Now().UTC()}
	for _, id := range ids {
		detail, err := h.service.GetTaskDetail(r.Context(), id)
		if err != nil {
			result.Failed++
			result.Targets = append(result.Targets, databaseInspectionTarget{TaskID: id, Status: "failed", Error: err.Error()})
			continue
		}
		target := databaseInspectionTarget{TaskID: id, MachineID: detail.Task.MachineID, Machine: detail.MachineName, IP: detail.MachineIP, Status: string(detail.Task.Status), StartedAt: timeValue(detail.Task.StartedAt), FinishedAt: timeValue(detail.Task.FinishedAt)}
		var spec taskdomain.ExecSpec
		if detail.Task.Type != taskdomain.TypeExec || json.Unmarshal(detail.Task.SpecJSON, &spec) != nil || (spec.Operation != "database_inspection" && spec.Operation != "database_deep_inspection") {
			target.Status, target.Error = "failed", "task is not a database inspection"
			result.Failed++
			result.Targets = append(result.Targets, target)
			continue
		}
		target.Port = spec.Port
		target.Level = "standard"
		if spec.Operation == "database_deep_inspection" {
			target.Level = "deep"
		}
		if machine, ok, machineErr := h.service.GetTaskMachine(r.Context(), detail.Task.MachineID); machineErr == nil && ok {
			target.Cluster = machine.Cluster
			if target.Machine == "" {
				target.Machine = machine.Name
			}
			if target.IP == "" {
				target.IP = machine.IP
			}
		}
		if detail.Task.Status != taskdomain.StatusSuccess && detail.Task.Status != taskdomain.StatusFailed {
			result.Ready = false
			result.Pending++
			result.Targets = append(result.Targets, target)
			continue
		}
		checks, hostname, version := parseDatabaseInspectionEvents(id, target, detail.Events)
		target.Hostname, target.Version = hostname, version
		if detail.Task.Status == taskdomain.StatusFailed {
			target.Error = automationTaskFailure(detail)
			result.Failed++
		} else if len(checks) == 0 {
			target.Status, target.Error = "failed", "inspection completed without structured output"
			result.Failed++
		}
		for _, check := range checks {
			switch check.Status {
			case "pass":
				target.Passed++
			case "warning":
				target.Warnings++
			case "critical", "failed":
				target.Critical++
			default:
				target.Information++
			}
		}
		target.Score = inspectionScore(target.Warnings, target.Critical)
		result.Checks = append(result.Checks, checks...)
		result.Targets = append(result.Targets, target)
	}
	sort.SliceStable(result.Targets, func(i, j int) bool {
		if result.Targets[i].Cluster != result.Targets[j].Cluster {
			return result.Targets[i].Cluster < result.Targets[j].Cluster
		}
		return result.Targets[i].IP < result.Targets[j].IP
	})
	return result, nil
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func parseDatabaseInspectionEvents(taskID string, target databaseInspectionTarget, events []taskdomain.Event) ([]databaseInspectionCheck, string, string) {
	checks := make([]databaseInspectionCheck, 0)
	hostname, version := "", ""
	for _, event := range events {
		for _, line := range strings.Split(strings.ReplaceAll(event.Content, "\r\n", "\n"), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "\t")
			if len(parts) >= 4 && parts[0] == "GMHA_INSPECTION_META" {
				hostname, version = parts[1], parts[2]
				if port, err := strconv.Atoi(parts[3]); err == nil {
					target.Port = port
				}
			}
			if len(parts) < 10 || parts[0] != "GMHA_INSPECTION_CHECK" {
				continue
			}
			checks = append(checks, databaseInspectionCheck{
				TaskID: taskID, Cluster: target.Cluster, Machine: target.Machine, IP: target.IP, Port: target.Port, Level: target.Level,
				Category: parts[1], Code: parts[2], Title: parts[3], Severity: strings.ToLower(parts[4]), Status: strings.ToLower(parts[5]),
				Value: parts[6], Threshold: parts[7], Description: parts[8], Recommendation: strings.Join(parts[9:], " "),
			})
		}
	}
	return checks, hostname, version
}

func inspectionScore(warnings, critical int) int {
	score := 100 - warnings*8 - critical*20
	if score < 0 {
		return 0
	}
	return score
}

func buildInspectionDOCX(result databaseInspectionResult) ([]byte, error) {
	var body strings.Builder
	body.WriteString(docxParagraph("GMHA 数据库巡检报告", "Title"))
	body.WriteString(docxParagraph("生成时间："+result.Exported.Local().Format("2006-01-02 15:04:05"), "Subtitle"))
	body.WriteString(docxParagraph(fmt.Sprintf("巡检实例 %d 个，完成 %d 个，失败 %d 个。", len(result.Targets), len(result.Targets)-result.Pending-result.Failed, result.Failed), "Normal"))
	for _, target := range result.Targets {
		level := "标准巡检"
		if target.Level == "deep" {
			level = "深度巡检"
		}
		body.WriteString(docxParagraph(fmt.Sprintf("%s · %s:%d", emptyAs(target.Cluster, "未分组"), target.IP, target.Port), "Heading1"))
		body.WriteString(docxParagraph(fmt.Sprintf("%s | MySQL %s | %s | 健康评分 %d", emptyAs(target.Machine, target.Hostname), emptyAs(target.Version, "未知"), level, target.Score), "Normal"))
		body.WriteString(docxTable([][]string{
			{"通过", "警告", "严重", "信息", "任务状态"},
			{strconv.Itoa(target.Passed), strconv.Itoa(target.Warnings), strconv.Itoa(target.Critical), strconv.Itoa(target.Information), inspectionStatusLabel(target.Status)},
		}))
		rows := [][]string{{"分类", "检查项", "状态", "当前值", "阈值", "说明与建议"}}
		for _, check := range result.Checks {
			if check.TaskID != target.TaskID {
				continue
			}
			rows = append(rows, []string{check.Category, check.Title, inspectionStatusLabel(check.Status), check.Value, check.Threshold, strings.TrimSpace(check.Description + "；" + check.Recommendation)})
		}
		if target.Error != "" {
			rows = append(rows, []string{"执行", "巡检任务", "失败", "", "", target.Error})
		}
		body.WriteString(docxTable(rows))
	}
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="900" w:right="700" w:bottom="900" w:left="700"/></w:sectPr>`)
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`
	files := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":            document,
		"word/styles.xml":              `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:rPr><w:rFonts w:eastAsia="Microsoft YaHei"/><w:sz w:val="20"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:pPr><w:jc w:val="center"/><w:spacing w:after="240"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:pPr><w:jc w:val="center"/></w:pPr><w:rPr><w:color w:val="667085"/><w:sz w:val="20"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:pPr><w:spacing w:before="280" w:after="120"/></w:pPr><w:rPr><w:b/><w:color w:val="175CD3"/><w:sz w:val="28"/></w:rPr></w:style></w:styles>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
	}
	return zipXMLFiles(files)
}

func buildInspectionXLSX(result databaseInspectionResult) ([]byte, error) {
	headers := []string{"集群", "机器", "IP", "端口", "巡检级别", "MySQL版本", "健康评分", "分类", "检查编码", "检查项", "严重级别", "状态", "当前值", "阈值", "检查说明", "整改建议", "任务ID"}
	rows := make([][]string, 0, len(result.Checks)+1)
	rows = append(rows, headers)
	targets := make(map[string]databaseInspectionTarget, len(result.Targets))
	for _, target := range result.Targets {
		targets[target.TaskID] = target
	}
	for _, check := range result.Checks {
		target := targets[check.TaskID]
		rows = append(rows, []string{target.Cluster, target.Machine, target.IP, strconv.Itoa(target.Port), inspectionLevelLabel(target.Level), target.Version, strconv.Itoa(target.Score), check.Category, check.Code, check.Title, check.Severity, inspectionStatusLabel(check.Status), check.Value, check.Threshold, check.Description, check.Recommendation, check.TaskID})
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`)
	for index := range headers {
		width := 16
		if index == 14 || index == 15 {
			width = 36
		}
		sheet.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%d" customWidth="1"/>`, index+1, index+1, width))
	}
	sheet.WriteString(`</cols><sheetData>`)
	for rowIndex, row := range rows {
		sheet.WriteString(fmt.Sprintf(`<row r="%d">`, rowIndex+1))
		for columnIndex, cell := range row {
			ref := spreadsheetColumn(columnIndex+1) + strconv.Itoa(rowIndex+1)
			style := 0
			if rowIndex == 0 {
				style = 1
			}
			sheet.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(cell)))
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(fmt.Sprintf(`</sheetData><autoFilter ref="A1:Q%d"/></worksheet>`, len(rows)))
	files := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="巡检明细" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
		"xl/styles.xml":              `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Microsoft YaHei"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF175CD3"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"><alignment vertical="top" wrapText="1"/></xf><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0"><alignment vertical="center"/></xf></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml":   sheet.String(),
	}
	return zipXMLFiles(files)
}

func docxParagraph(text, style string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + xmlEscape(style) + `"/></w:pPr><w:r><w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r></w:p>`
}

func docxTable(rows [][]string) string {
	var result strings.Builder
	result.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders><w:top w:val="single" w:sz="4" w:color="D0D5DD"/><w:left w:val="single" w:sz="4" w:color="D0D5DD"/><w:bottom w:val="single" w:sz="4" w:color="D0D5DD"/><w:right w:val="single" w:sz="4" w:color="D0D5DD"/><w:insideH w:val="single" w:sz="4" w:color="D0D5DD"/><w:insideV w:val="single" w:sz="4" w:color="D0D5DD"/></w:tblBorders></w:tblPr>`)
	for rowIndex, row := range rows {
		result.WriteString(`<w:tr>`)
		for _, cell := range row {
			result.WriteString(`<w:tc><w:tcPr>`)
			if rowIndex == 0 {
				result.WriteString(`<w:shd w:fill="EAF2FF"/>`)
			}
			result.WriteString(`</w:tcPr><w:p><w:r>`)
			if rowIndex == 0 {
				result.WriteString(`<w:rPr><w:b/></w:rPr>`)
			}
			result.WriteString(`<w:t xml:space="preserve">` + xmlEscape(cell) + `</w:t></w:r></w:p></w:tc>`)
		}
		result.WriteString(`</w:tr>`)
	}
	result.WriteString(`</w:tbl>`)
	return result.String()
}

func zipXMLFiles(files map[string]string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func xmlEscape(value string) string {
	var result strings.Builder
	for _, r := range value {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			switch r {
			case '&':
				result.WriteString("&amp;")
			case '<':
				result.WriteString("&lt;")
			case '>':
				result.WriteString("&gt;")
			case '"':
				result.WriteString("&quot;")
			case '\'':
				result.WriteString("&apos;")
			default:
				result.WriteRune(r)
			}
		}
	}
	return result.String()
}

func spreadsheetColumn(index int) string {
	var result string
	for index > 0 {
		index--
		result = string(rune('A'+index%26)) + result
		index /= 26
	}
	return result
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func inspectionLevelLabel(level string) string {
	if level == "deep" {
		return "深度巡检"
	}
	return "标准巡检"
}

func inspectionStatusLabel(status string) string {
	switch strings.ToLower(status) {
	case "pass", "success":
		return "通过"
	case "warning":
		return "警告"
	case "critical", "failed":
		return "严重"
	case "pending", "sent", "running":
		return "执行中"
	case "info":
		return "信息"
	default:
		return status
	}
}
