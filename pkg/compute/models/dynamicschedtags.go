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
	"database/sql"
	"fmt"

	"yunion.io/x/jsonutils"
	"yunion.io/x/log"
	"yunion.io/x/pkg/errors"
	"yunion.io/x/pkg/tristate"
	"yunion.io/x/sqlchemy"

	"yunion.io/x/onecloud/pkg/apis"
	api "yunion.io/x/onecloud/pkg/apis/compute"
	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
	"yunion.io/x/onecloud/pkg/util/conditionparser"
	"yunion.io/x/onecloud/pkg/util/stringutils2"
)

type IDynamicResourceManager interface {
	db.IModelManager
}

type IDynamicResource interface {
	db.IModel
	GetDynamicConditionInput() *jsonutils.JSONDict
}

type SDynamicschedtagManager struct {
	db.SStandaloneResourceBaseManager
	SSchedtagResourceBaseManager

	StandaloneResourcesManager map[string]IDynamicResourceManager
	VirtualResourcesManager    map[string]IDynamicResourceManager
}

var DynamicschedtagManager *SDynamicschedtagManager

func init() {
	DynamicschedtagManager = &SDynamicschedtagManager{
		SStandaloneResourceBaseManager: db.NewStandaloneResourceBaseManager(
			SDynamicschedtag{},
			"dynamicschedtags_tbl",
			"dynamicschedtag",
			"dynamicschedtags",
		),
		StandaloneResourcesManager: make(map[string]IDynamicResourceManager),
		VirtualResourcesManager:    make(map[string]IDynamicResourceManager),
	}
	DynamicschedtagManager.SetVirtualObject(DynamicschedtagManager)
}

func (man *SDynamicschedtagManager) bindDynamicResourceManager(
	store map[string]IDynamicResourceManager,
	ms ...IDynamicResourceManager) {
	for _, m := range ms {
		store[m.Keyword()] = m
	}
}

func (man *SDynamicschedtagManager) BindStandaloneResourceManager(ms ...IDynamicResourceManager) {
	man.bindDynamicResourceManager(man.StandaloneResourcesManager, ms...)
}

func (man *SDynamicschedtagManager) BindVirtualResourceManager(ms ...IDynamicResourceManager) {
	man.bindDynamicResourceManager(man.VirtualResourcesManager, ms...)
}

func (man *SDynamicschedtagManager) InitializeData() error {
	man.BindStandaloneResourceManager(
		HostManager,
		StorageManager,
	)
	man.BindVirtualResourceManager(
		GuestManager,
		DiskManager,
	)
	return nil
}

// dynamic schedtag is called before scan host candidates, dynamically adding additional schedtag to hosts
// condition examples:
//  host.sys_load > 1.5 || host.mem_used_percent > 0.7 => "high_load"
//
type SDynamicschedtag struct {
	db.SStandaloneResourceBase
	SSchedtagResourceBase `width:"36" charset:"ascii" nullable:"false" list:"user" create:"required" update:"admin"`

	// 动态调度标间的匹配条件
	// example: host.sys_load > 1.5 || host.mem_used_percent > 0.7 => "high_load"
	Condition string `width:"1024" charset:"ascii" nullable:"false" list:"user" create:"required" update:"admin"`

	// 动态调度标签对应的调度标签
	// SchedtagId string `width:"36" charset:"ascii" nullable:"false" list:"user" create:"required" update:"admin"`

	Enabled tristate.TriState `nullable:"false" default:"true" create:"optional" list:"user" update:"user"`
}

func validateDynamicSchedtagInputData(data *jsonutils.JSONDict, create bool) error {
	condStr := jsonutils.GetAnyString(data, []string{"condition"})
	if len(condStr) == 0 && create {
		return httperrors.NewMissingParameterError("condition")
	}
	if len(condStr) > 0 && !conditionparser.IsValid(condStr) {
		return httperrors.NewInputParameterError("invalid condition")
	}

	schedStr := jsonutils.GetAnyString(data, []string{"schedtag", "schedtag_id"})
	if len(schedStr) == 0 && create {
		return httperrors.NewMissingParameterError("schedtag_id")
	}
	if len(schedStr) > 0 {
		schedObj, err := SchedtagManager.FetchByIdOrName(nil, schedStr)
		if err != nil {
			if err == sql.ErrNoRows {
				return httperrors.NewResourceNotFoundError("schedtag %s not found", schedStr)
			} else {
				log.Errorf("fetch schedtag %s fail %s", schedStr, err)
				return httperrors.NewGeneralError(err)
			}
		}
		schedtag := schedObj.(*SSchedtag)
		data.Set("schedtag_id", jsonutils.NewString(schedtag.GetId()))
	}

	return nil
}

func (manager *SDynamicschedtagManager) ValidateCreateData(ctx context.Context, userCred mcclient.TokenCredential, ownerId mcclient.IIdentityProvider, query jsonutils.JSONObject, data *jsonutils.JSONDict) (*jsonutils.JSONDict, error) {
	err := validateDynamicSchedtagInputData(data, true)
	if err != nil {
		return nil, err
	}
	input := apis.StandaloneResourceCreateInput{}
	err = data.Unmarshal(&input)
	if err != nil {
		return nil, httperrors.NewInternalServerError("unmarshal StandaloneResourceCreateInput fail %s", err)
	}
	input, err = manager.SStandaloneResourceBaseManager.ValidateCreateData(ctx, userCred, ownerId, query, input)
	if err != nil {
		return nil, err
	}
	data.Update(jsonutils.Marshal(input))
	return data, nil
}

func (self *SDynamicschedtag) ValidateUpdateData(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data *jsonutils.JSONDict) (*jsonutils.JSONDict, error) {
	err := validateDynamicSchedtagInputData(data, false)
	if err != nil {
		return nil, err
	}

	input := apis.StandaloneResourceBaseUpdateInput{}
	err = data.Unmarshal(&input)
	if err != nil {
		return nil, errors.Wrap(err, "Unmarshal")
	}
	input, err = self.SStandaloneResourceBase.ValidateUpdateData(ctx, userCred, query, input)
	if err != nil {
		return nil, errors.Wrap(err, "SStandaloneResourceBase.ValidateUpdateData")
	}
	data.Update(jsonutils.Marshal(input))

	return data, nil
}

func (manager *SDynamicschedtagManager) FetchCustomizeColumns(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	query jsonutils.JSONObject,
	objs []interface{},
	fields stringutils2.SSortedStrings,
	isList bool,
) []api.DynamicschedtagDetails {
	rows := make([]api.DynamicschedtagDetails, len(objs))

	stdRows := manager.SStandaloneResourceBaseManager.FetchCustomizeColumns(ctx, userCred, query, objs, fields, isList)
	tagRows := manager.SSchedtagResourceBaseManager.FetchCustomizeColumns(ctx, userCred, query, objs, fields, isList)

	for i := range rows {
		rows[i] = api.DynamicschedtagDetails{
			StandaloneResourceDetails: stdRows[i],
			SchedtagResourceInfo:      tagRows[i],
		}
	}

	return rows
}

func (manager *SDynamicschedtagManager) GetEnabledDynamicSchedtagsByResource(resType string) []SDynamicschedtag {
	rules := make([]SDynamicschedtag, 0)

	q := DynamicschedtagManager.Query().IsTrue("enabled")
	schedtags := SchedtagManager.Query().SubQuery()
	q = q.Join(schedtags, sqlchemy.AND(
		sqlchemy.Equals(q.Field("schedtag_id"), schedtags.Field("id")),
		sqlchemy.Equals(schedtags.Field("resource_type"), resType)))
	err := db.FetchModelObjects(manager, q, &rules)
	if err != nil {
		log.Errorf("GetEnabledDynamicSchedtagsByResource %s fail %s", resType, err)
		return nil
	}

	return rules
}

func (self *SDynamicschedtag) PerformEvaluate(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) (jsonutils.JSONObject, error) {
	objectId := jsonutils.GetAnyString(data, []string{"object", "object_id"})
	resType := jsonutils.GetAnyString(data, []string{"resource_type"})
	virtObjId := jsonutils.GetAnyString(data, []string{"virtual_object", "virtual_object_id"})
	virtType := jsonutils.GetAnyString(data, []string{"virtual_resource_type"})

	objectMan := DynamicschedtagManager.StandaloneResourcesManager[resType]
	if objectMan == nil {
		return nil, httperrors.NewResourceNotFoundError("Resource type %s not support", resType)
	}
	virtObjectMan := DynamicschedtagManager.VirtualResourcesManager[virtType]
	if virtObjectMan == nil {
		return nil, httperrors.NewResourceNotFoundError("Virtual resource type %s not support", virtType)
	}

	object, err := FetchDynamicResourceObject(objectMan, userCred, objectId)
	if err != nil {
		return nil, err
	}
	virtObject, err := FetchDynamicResourceObject(virtObjectMan, userCred, virtObjId)
	if err != nil {
		return nil, err
	}

	// TODO: to fill standalone resource scheduling information
	standaloneDesc := object.GetDynamicConditionInput()
	virtDesc := virtObject.GetDynamicConditionInput()

	params := jsonutils.NewDict()
	params.Add(standaloneDesc, object.Keyword())
	params.Add(virtDesc, virtObject.Keyword())

	log.V(10).Debugf("Dynamicschedtag evaluate input: %s", params.PrettyString())

	meet, err := conditionparser.EvalBool(self.Condition, params)
	if err != nil {
		return nil, err
	}
	result := jsonutils.NewDict()
	result.Add(standaloneDesc, object.Keyword())
	result.Add(virtDesc, virtObject.Keyword())

	if meet {
		result.Add(jsonutils.JSONTrue, "result")
	} else {
		result.Add(jsonutils.JSONFalse, "result")
	}
	return result, nil
}

func FetchDynamicResourceObject(man IDynamicResourceManager, userCred mcclient.TokenCredential, idOrName string) (IDynamicResource, error) {
	obj, err := man.FetchByIdOrName(userCred, idOrName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, httperrors.NewResourceNotFoundError("%s %s not found", man.Keyword(), idOrName)
		} else {
			return nil, httperrors.NewGeneralError(err)
		}
	}
	res, ok := obj.(IDynamicResource)
	if !ok {
		return nil, httperrors.NewGeneralError(fmt.Errorf("%s %s not implement IDynamicResource", obj.Keyword(), obj.GetName()))
	}
	return res, nil
}

// 动态调度标签列表
func (manager *SDynamicschedtagManager) ListItemFilter(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	input api.DynamicschedtagListInput,
) (*sqlchemy.SQuery, error) {
	var err error

	q, err = manager.SStandaloneResourceBaseManager.ListItemFilter(ctx, q, userCred, input.StandaloneResourceListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SStandaloneResourceBaseManager.ListItemFilter")
	}
	q, err = manager.SSchedtagResourceBaseManager.ListItemFilter(ctx, q, userCred, input.SchedtagFilterListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SSchedtagResourceBaseManager.ListItemFilter")
	}

	if input.Enabled != nil {
		if *input.Enabled {
			q = q.IsTrue("enabled")
		} else {
			q = q.IsFalse("enabled")
		}
	}

	return q, nil
}

func (manager *SDynamicschedtagManager) OrderByExtraFields(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	input api.DynamicschedtagListInput,
) (*sqlchemy.SQuery, error) {
	var err error

	q, err = manager.SStandaloneResourceBaseManager.OrderByExtraFields(ctx, q, userCred, input.StandaloneResourceListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SStandaloneResourceBaseManager.OrderByExtraFields")
	}

	q, err = manager.SSchedtagResourceBaseManager.OrderByExtraFields(ctx, q, userCred, input.SchedtagFilterListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SSchedtagResourceBaseManager.OrderByExtraFields(")
	}

	return q, nil
}

func (manager *SDynamicschedtagManager) QueryDistinctExtraField(q *sqlchemy.SQuery, field string) (*sqlchemy.SQuery, error) {
	var err error

	q, err = manager.SStandaloneResourceBaseManager.QueryDistinctExtraField(q, field)
	if err == nil {
		return q, nil
	}
	q, err = manager.SSchedtagResourceBaseManager.QueryDistinctExtraField(q, field)
	if err == nil {
		return q, nil
	}

	return q, httperrors.ErrNotFound
}
