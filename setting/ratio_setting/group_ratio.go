package ratio_setting

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
)

var defaultGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

var groupRatioMap = types.NewRWMap[string, float64]()

var defaultGroupGroupRatio = map[string]map[string]float64{
	"vip": {
		"edit_this": 0.9,
	},
}

var groupGroupRatioMap = types.NewRWMap[string, map[string]float64]()

var groupGroupRatioAdjustmentMap = types.NewRWMap[string, map[string]float64]()

var defaultGroupSpecialUsableGroup = map[string]map[string]string{}

type GroupRatioSetting struct {
	GroupRatio                *types.RWMap[string, float64]            `json:"group_ratio"`
	GroupGroupRatio           *types.RWMap[string, map[string]float64] `json:"group_group_ratio"`
	GroupGroupRatioAdjustment *types.RWMap[string, map[string]float64] `json:"group_group_ratio_adjustment"`
	GroupSpecialUsableGroup   *types.RWMap[string, map[string]string]  `json:"group_special_usable_group"`
}

var groupRatioSetting GroupRatioSetting

func init() {
	groupSpecialUsableGroup := types.NewRWMap[string, map[string]string]()
	groupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)

	groupRatioMap.AddAll(defaultGroupRatio)
	groupGroupRatioMap.AddAll(defaultGroupGroupRatio)
	groupGroupRatioAdjustmentMap.AddAll(map[string]map[string]float64{})

	groupRatioSetting = GroupRatioSetting{
		GroupSpecialUsableGroup:   groupSpecialUsableGroup,
		GroupRatio:                groupRatioMap,
		GroupGroupRatio:           groupGroupRatioMap,
		GroupGroupRatioAdjustment: groupGroupRatioAdjustmentMap,
	}

	config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)
}

func GetGroupRatioSetting() *GroupRatioSetting {
	if groupRatioSetting.GroupSpecialUsableGroup == nil {
		groupRatioSetting.GroupSpecialUsableGroup = types.NewRWMap[string, map[string]string]()
		groupRatioSetting.GroupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)
	}
	return &groupRatioSetting
}

func GetGroupRatioCopy() map[string]float64 {
	return groupRatioMap.ReadAll()
}

func ContainsGroupRatio(name string) bool {
	_, ok := groupRatioMap.Get(name)
	return ok
}

func GroupRatio2JSONString() string {
	return groupRatioMap.MarshalJSONString()
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(groupRatioMap, jsonStr)
}

func GetGroupRatio(name string) float64 {
	ratio, ok := groupRatioMap.Get(name)
	if !ok {
		common.SysLog("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
	gp, ok := groupGroupRatioMap.Get(userGroup)
	if !ok {
		return -1, false
	}
	ratio, ok := gp[usingGroup]
	if !ok {
		return -1, false
	}
	return ratio, true
}

func GetEffectiveGroupRatio(userGroup, usingGroup string) (float64, bool) {
	baseRatio := GetGroupRatio(usingGroup)
	if groupRatios, ok := groupGroupRatioAdjustmentMap.Get(userGroup); ok {
		if adjustment, ok := groupRatios[usingGroup]; ok {
			return max(baseRatio-adjustment, 0), true
		}
	}
	if ratio, ok := GetGroupGroupRatio(userGroup, usingGroup); ok {
		return ratio, true
	}
	return baseRatio, false
}

func GroupGroupRatio2JSONString() string {
	return groupGroupRatioMap.MarshalJSONString()
}

func UpdateGroupGroupRatioByJSONString(jsonStr string) error {
	if err := CheckGroupGroupRatio(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonString(groupGroupRatioMap, jsonStr)
}

func GroupGroupRatioAdjustment2JSONString() string {
	return groupGroupRatioAdjustmentMap.MarshalJSONString()
}

func UpdateGroupGroupRatioAdjustmentByJSONString(jsonStr string) error {
	if err := CheckGroupGroupRatioAdjustment(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonString(groupGroupRatioAdjustmentMap, jsonStr)
}

func CheckGroupRatio(jsonStr string) error {
	checkGroupRatio := make(map[string]float64)
	err := common.UnmarshalJsonStr(jsonStr, &checkGroupRatio)
	if err != nil {
		return err
	}
	for name, ratio := range checkGroupRatio {
		if ratio < 0 {
			return errors.New("group ratio must be not less than 0: " + name)
		}
	}
	return nil
}

func CheckGroupGroupRatio(jsonStr string) error {
	checkGroupGroupRatio := make(map[string]map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &checkGroupGroupRatio); err != nil {
		return err
	}
	for userGroup, groupRatios := range checkGroupGroupRatio {
		for billingGroup, ratio := range groupRatios {
			if ratio < 0 {
				return errors.New("downstream ratio must not be less than 0: " + userGroup + "/" + billingGroup)
			}
		}
	}
	return nil
}

func CheckGroupGroupRatioAdjustment(jsonStr string) error {
	checkGroupGroupRatio := make(map[string]map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &checkGroupGroupRatio); err != nil {
		return err
	}
	for userGroup, groupRatios := range checkGroupGroupRatio {
		for billingGroup, adjustment := range groupRatios {
			if adjustment < 0 {
				return errors.New("downstream ratio adjustment must not be less than 0: " + userGroup + "/" + billingGroup)
			}
		}
	}
	return nil
}
