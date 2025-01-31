// Copyright 2019 Yunion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"yunion.io/x/jsonutils"
	"yunion.io/x/pkg/errors"
	"yunion.io/x/pkg/util/regutils"
	"yunion.io/x/sqlchemy"

	"yunion.io/x/onecloud/pkg/apis"
	api "yunion.io/x/onecloud/pkg/apis/compute"
	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
	"yunion.io/x/onecloud/pkg/util/stringutils2"
)

type SDnsRecordManager struct {
	db.SAdminSharableVirtualResourceBaseManager
	db.SEnabledResourceBaseManager
}

var _ db.IAdminSharableVirtualModelManager = DnsRecordManager

var DnsRecordManager *SDnsRecordManager

func init() {
	DnsRecordManager = &SDnsRecordManager{
		SAdminSharableVirtualResourceBaseManager: db.NewAdminSharableVirtualResourceBaseManager(
			SDnsRecord{},
			"dnsrecord_tbl",
			"dnsrecord",
			"dnsrecords",
		),
	}
	DnsRecordManager.SetVirtualObject(DnsRecordManager)
}

const DNS_RECORDS_SEPARATOR = ","

type SDnsRecord struct {
	db.SAdminSharableVirtualResourceBase
	db.SEnabledResourceBase `nullable:"false" default:"true" create:"optional" list:"user"`

	// DNS记录的过期时间，单位为秒
	// example: 60
	Ttl int `nullable:"true" default:"1" create:"optional" list:"user" update:"user" json:"ttl"`

	//Enabled tristate.TriState `nullable:"false" default:"true" create:"optional" list:"user"`
}

// GetRecordsSeparator implements IAdminSharableVirtualModelManager
func (man *SDnsRecordManager) GetRecordsSeparator() string {
	return DNS_RECORDS_SEPARATOR
}

// GetRecordsLimit implements IAdminSharableVirtualModelManager
func (man *SDnsRecordManager) GetRecordsLimit() int {
	return 0
}

// ParseInputInfo implements IAdminSharableVirtualModelManager
func (man *SDnsRecordManager) ParseInputInfo(data *jsonutils.JSONDict) ([]string, error) {
	records := []string{}
	for _, typ := range []string{"A", "AAAA"} {
		for i := 0; ; i++ {
			key := fmt.Sprintf("%s.%d", typ, i)
			if !data.Contains(key) {
				break
			}
			addr, err := data.GetString(key)
			if err != nil {
				return nil, err
			}
			if err := man.checkRecordValue(typ, addr); err != nil {
				return nil, err
			}
			records = append(records, fmt.Sprintf("%s:%s", typ, addr))
		}
	}
	{
		// - SRV.i
		// - (deprecated) SRV_host and SRV_port
		//
		// - rfc2782, A DNS RR for specifying the location of services (DNS SRV),
		//   https://tools.ietf.org/html/rfc2782
		parseSrvParam := func(s string) (string, error) {
			parts := strings.SplitN(s, ":", 4)
			if len(parts) < 2 {
				return "", httperrors.NewNotAcceptableError("SRV: insufficient param: %s", s)
			}
			host := parts[0]
			if err := man.checkRecordValue("SRV", host); err != nil {
				return "", err
			}
			port, err := strconv.Atoi(parts[1])
			if err != nil || port <= 0 || port >= 65536 {
				return "", httperrors.NewNotAcceptableError("SRV: invalid port number: %s", parts[1])
			}
			weight := 100
			priority := 0
			if len(parts) >= 3 {
				var err error
				weight, err = strconv.Atoi(parts[2])
				if err != nil {
					return "", httperrors.NewNotAcceptableError("SRV: invalid weight number: %s", parts[2])
				}
				if weight < 0 || weight > 65535 {
					return "", httperrors.NewNotAcceptableError("SRV: weight number %d not in range [0,65535]", weight)
				}
				if len(parts) >= 4 {
					priority, err = strconv.Atoi(parts[3])
					if err != nil {
						return "", httperrors.NewNotAcceptableError("SRV: invalid priority number: %s", parts[3])
					}
					if priority < 0 || priority > 65535 {
						return "", httperrors.NewNotAcceptableError("SRV: priority number %d not in range [0,65535]", priority)
					}
				}
			}
			rec := fmt.Sprintf("SRV:%s:%d:%d:%d", host, port, weight, priority)
			return rec, nil
		}
		recSrv := []string{}
		for i := 0; ; i++ {
			k := fmt.Sprintf("SRV.%d", i)
			if !data.Contains(k) {
				break
			}
			s, err := data.GetString(k)
			if err != nil {
				return nil, err
			}
			rec, err := parseSrvParam(s)
			if err != nil {
				return nil, err
			}
			recSrv = append(recSrv, rec)
		}
		if data.Contains("SRV_host") && data.Contains("SRV_port") {
			host, err := data.GetString("SRV_host")
			if err != nil {
				return nil, err
			}
			port, err := data.GetString("SRV_port")
			if err != nil {
				return nil, err
			}
			s := fmt.Sprintf("%s:%s", host, port)
			rec, err := parseSrvParam(s)
			if err != nil {
				return nil, err
			}
			recSrv = append(recSrv, rec)
		}
		if len(recSrv) > 0 {
			if len(records) > 0 {
				return nil, httperrors.NewNotAcceptableError("SRV cannot mix with other types")
			}
			records = recSrv
		}
	}
	if data.Contains("CNAME") {
		if len(records) > 0 {
			return nil, httperrors.NewNotAcceptableError("CNAME cannot mix with other types")
		}
		if cname, err := data.GetString("CNAME"); err != nil {
			return nil, err
		} else if err := man.checkRecordValue("CNAME", cname); err != nil {
			return nil, err
		} else {
			records = []string{fmt.Sprintf("%s:%s", "CNAME", cname)}
		}
	}
	if data.Contains("PTR") {
		if len(records) > 0 {
			return nil, httperrors.NewNotAcceptableError("PTR cannot mix with other types")
		}
		name, err := data.GetString("name")
		{
			if err != nil {
				return nil, err
			}
			if err := man.checkRecordName("PTR", name); err != nil {
				return nil, err
			}
		}
		domainName, err := data.GetString("PTR")
		{
			if err != nil {
				return nil, err
			}
			if err := man.checkRecordValue("PTR", domainName); err != nil {
				return nil, err
			}
		}
		records = []string{fmt.Sprintf("%s:%s", "PTR", domainName)}
	}
	return records, nil
}

func (man *SDnsRecordManager) getRecordsType(recs []string) string {
	for _, rec := range recs {
		switch typ := rec[:strings.Index(rec, ":")]; typ {
		case "A", "AAAA":
			return "A"
		case "CNAME":
			return "CNAME"
		case "SRV":
			return "SRV"
		case "PTR":
			return "PTR"
		}
	}
	return ""
}

func (man *SDnsRecordManager) checkRecordName(typ, name string) error {
	switch typ {
	case "A", "CNAME":
		if !regutils.MatchDomainName(name) {
			return httperrors.NewNotAcceptableError("%s: invalid domain name: %s", typ, name)
		}
	case "SRV":
		if !regutils.MatchDomainSRV(name) {
			return httperrors.NewNotAcceptableError("SRV: invalid srv record name: %s", name)
		}
	case "PTR":
		if !regutils.MatchPtr(name) {
			return httperrors.NewNotAcceptableError("PTR: invalid ptr record name: %s", name)
		}
	}
	if regutils.MatchIPAddr(name) {
		return httperrors.NewNotAcceptableError("%s: name cannot be ip address: %s", typ, name)
	}
	return nil
}

func (man *SDnsRecordManager) checkRecordValue(typ, val string) error {
	switch typ {
	case "A":
		if !regutils.MatchIP4Addr(val) {
			return httperrors.NewNotAcceptableError("A: record value must be ipv4 address: %s", val)
		}
	case "AAAA":
		if !regutils.MatchIP6Addr(val) {
			return httperrors.NewNotAcceptableError("AAAA: record value must be ipv6 address: %s", val)
		}
	case "CNAME", "PTR", "SRV":
		fieldMsg := "record value"
		if typ == "SRV" {
			fieldMsg = "target"
		}
		if !regutils.MatchDomainName(val) {
			return httperrors.NewNotAcceptableError("%s: %s must be domain name: %s", typ, fieldMsg, val)
		}
		if regutils.MatchIPAddr(val) {
			return httperrors.NewNotAcceptableError("%s: %s cannot be ip address: %s", typ, fieldMsg, val)
		}
	default:
		// internal error
		return httperrors.NewNotAcceptableError("%s: unknown record type", typ)
	}
	return nil
}

func (man *SDnsRecordManager) validateModelData(
	ctx context.Context,
	data *jsonutils.JSONDict,
	isCreate bool,
) (records []string, err error) {
	data.Remove("records")
	records, err = man.ParseInputInfo(data)
	if err != nil {
		return
	}
	if len(records) == 0 {
		if isCreate {
			err = httperrors.NewInputParameterError("Empty record")
			return
		}
		return
	}
	recType := man.getRecordsType(records)
	name, err := data.GetString("name")
	if err != nil {
		return
	}
	err = man.checkRecordName(recType, name)
	if err != nil {
		return
	}
	if data.Contains("ttl") {
		var (
			ttl int64
		)
		ttl, err = data.Int("ttl")
		if err != nil {
			err = httperrors.NewInputParameterError("invalid ttl: %s", err)
			return
		}
		if ttl == 0 {
			// - Create: use the database default
			// - Update: unchanged
			data.Remove("ttl")
		} else if ttl < 0 || ttl > 0x7fffffff {
			// positive values of a signed 32 bit number.
			err = httperrors.NewInputParameterError("invalid ttl: %d", ttl)
			return
		}
	}
	return records, nil
}

func (man *SDnsRecordManager) ValidateCreateData(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	ownerId mcclient.IIdentityProvider,
	query jsonutils.JSONObject,
	data *jsonutils.JSONDict,
) (*jsonutils.JSONDict, error) {
	var err error

	input := apis.AdminSharableVirtualResourceBaseCreateInput{}
	err = data.Unmarshal(&input)
	if err != nil {
		return nil, errors.Wrap(err, "Unmarshal AdminSharableVirtualResourceBaseCreateInput")
	}

	input, err = man.SAdminSharableVirtualResourceBaseManager.ValidateCreateData(ctx, userCred, ownerId, query, input)
	if err != nil {
		return nil, errors.Wrap(err, "SAdminSharableVirtualResourceBaseManager.ValidateCreateData")
	}

	data.Update(jsonutils.Marshal(input))

	_, err = man.validateModelData(ctx, data, true)
	if err != nil {
		return nil, err
	}
	return man.SAdminSharableVirtualResourceBaseManager.ValidateRecordsData(man, data)
}

func (man *SDnsRecordManager) QueryDns(projectId, name string) *SDnsRecord {
	q := man.Query().
		Equals("name", name).
		IsTrue("enabled")
	if len(projectId) == 0 {
		q = q.IsTrue("is_public")
	} else {
		q = q.Filter(sqlchemy.OR(
			sqlchemy.IsTrue(q.Field("is_public")),
			sqlchemy.Equals(q.Field("tenant_id"), projectId),
		))
	}
	rec := &SDnsRecord{}
	rec.SetModelManager(DnsRecordManager, rec)
	if err := q.First(rec); err != nil {
		return nil
	}
	return rec
}

type DnsIp struct {
	Addr string
	Ttl  int
}

func (man *SDnsRecordManager) QueryDnsIps(projectId, name, kind string) []*DnsIp {
	rec := man.QueryDns(projectId, name)
	if rec == nil {
		return nil
	}
	pref := kind + ":"
	prefLen := len(pref)
	dnsIps := []*DnsIp{}
	for _, r := range rec.GetInfo() {
		if strings.HasPrefix(r, pref) {
			dnsIps = append(dnsIps, &DnsIp{
				Addr: r[prefLen:],
				Ttl:  rec.Ttl,
			})
		}
	}
	return dnsIps
}

func (rec *SDnsRecord) IsCNAME() bool {
	return strings.HasPrefix(rec.Records, "CNAME:")
}

func (rec *SDnsRecord) HasRecordType(typ string) bool {
	for _, r := range rec.GetInfo() {
		if strings.HasPrefix(r, typ+":") {
			return true
		}
	}
	return false
}

func (rec *SDnsRecord) GetCNAME() string {
	if !rec.IsCNAME() {
		panic("not a cname record: " + rec.Records)
	}
	return rec.Records[len("CNAME:"):]
}

func (rec *SDnsRecord) GetInfo() []string {
	return strings.Split(rec.Records, DNS_RECORDS_SEPARATOR)
}

func (rec *SDnsRecord) ValidateUpdateData(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data *jsonutils.JSONDict) (*jsonutils.JSONDict, error) {
	data.UpdateDefault(jsonutils.Marshal(rec))
	records, err := DnsRecordManager.validateModelData(ctx, data, false)
	if err != nil {
		return nil, err
	}
	if len(records) > 0 {
		data.Set("records", jsonutils.NewString(strings.Join(records, DNS_RECORDS_SEPARATOR)))
	}
	input := apis.AdminSharableVirtualResourceBaseUpdateInput{}
	err = data.Unmarshal(&input)
	if err != nil {
		return nil, errors.Wrap(err, "data.Unmarshal AdminSharableVirtualResourceBaseUpdateInput")
	}
	input, err = rec.SAdminSharableVirtualResourceBase.ValidateUpdateData(ctx, userCred, query, input)
	if err != nil {
		return nil, errors.Wrap(err, "SAdminSharableVirtualResourceBase.ValidateUpdateData")
	}
	data.Update(jsonutils.Marshal(input))
	return data, nil
}

func (rec *SDnsRecord) AddInfo(ctx context.Context, userCred mcclient.TokenCredential, data jsonutils.JSONObject) error {
	return rec.SAdminSharableVirtualResourceBase.AddInfo(ctx, userCred, DnsRecordManager, rec, data)
}

func (rec *SDnsRecord) PerformAddRecords(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) (jsonutils.JSONObject, error) {
	records, err := DnsRecordManager.ParseInputInfo(data.(*jsonutils.JSONDict))
	if err != nil {
		return nil, err
	}
	oldRecs := rec.GetInfo()
	oldType := DnsRecordManager.getRecordsType(oldRecs)
	newType := DnsRecordManager.getRecordsType(records)
	if oldType != "" && oldType != newType {
		return nil, httperrors.NewNotAcceptableError("Cannot mix different types of records, %s != %s", oldType, newType)
	}
	err = rec.AddInfo(ctx, userCred, data)
	return nil, err
}

func (rec *SDnsRecord) PerformRemoveRecords(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) (jsonutils.JSONObject, error) {
	err := rec.SAdminSharableVirtualResourceBase.RemoveInfo(ctx, userCred, DnsRecordManager, rec, data, false)
	return nil, err
}

func (rec *SDnsRecord) PerformEnable(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, input apis.PerformEnableInput) (jsonutils.JSONObject, error) {
	err := db.EnabledPerformEnable(rec, ctx, userCred, true)
	if err != nil {
		return nil, errors.Wrap(err, "db.EnabledPerformEnable")
	}
	return nil, nil
}

func (rec *SDnsRecord) PerformDisable(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, input apis.PerformDisableInput) (jsonutils.JSONObject, error) {
	err := db.EnabledPerformEnable(rec, ctx, userCred, false)
	if err != nil {
		return nil, errors.Wrap(err, "db.EnabledPerformEnable")
	}
	return nil, nil
}

// 域名记录列表
func (manager *SDnsRecordManager) ListItemFilter(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	query api.DnsRecordListInput,
) (*sqlchemy.SQuery, error) {
	var err error
	q, err = manager.SAdminSharableVirtualResourceBaseManager.ListItemFilter(ctx, q, userCred, query.AdminSharableVirtualResourceListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SAdminSharableVirtualResourceBaseManager.ListItemFilter")
	}
	return q, nil
}

func (manager *SDnsRecordManager) OrderByExtraFields(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	query api.DnsRecordListInput,
) (*sqlchemy.SQuery, error) {
	var err error
	q, err = manager.SAdminSharableVirtualResourceBaseManager.OrderByExtraFields(ctx, q, userCred, query.AdminSharableVirtualResourceListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SAdminSharableVirtualResourceBaseManager.OrderByExtraFields")
	}
	return q, nil
}

func (manager *SDnsRecordManager) QueryDistinctExtraField(q *sqlchemy.SQuery, field string) (*sqlchemy.SQuery, error) {
	var err error
	q, err = manager.SAdminSharableVirtualResourceBaseManager.QueryDistinctExtraField(q, field)
	if err == nil {
		return q, nil
	}
	return q, httperrors.ErrNotFound
}

func (manager *SDnsRecordManager) FetchCustomizeColumns(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	query jsonutils.JSONObject,
	objs []interface{},
	fields stringutils2.SSortedStrings,
	isList bool,
) []api.DnsRecordDetails {
	rows := make([]api.DnsRecordDetails, len(objs))

	virtRows := manager.SAdminSharableVirtualResourceBaseManager.FetchCustomizeColumns(ctx, userCred, query, objs, fields, isList)
	for i := range rows {
		rows[i] = api.DnsRecordDetails{
			AdminSharableVirtualResourceDetails: virtRows[i],
		}
	}

	return rows
}
