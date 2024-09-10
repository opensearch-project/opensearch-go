// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"context"
	"encoding/json"
)

type policiesClient struct {
	apiClient *Client
}

// Get executes a get policies request with the optional PoliciesGetReq
func (c policiesClient) Get(ctx context.Context, req *PoliciesGetReq) (PoliciesGetResp, error) {
	if req == nil {
		req = &PoliciesGetReq{}
	}

	var (
		data PoliciesGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put policies request with the required PoliciesPutReq
func (c policiesClient) Put(ctx context.Context, req PoliciesPutReq) (PoliciesPutResp, error) {
	var (
		data PoliciesPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete policies request with the required PoliciesDeleteReq
func (c policiesClient) Delete(ctx context.Context, req PoliciesDeleteReq) (PoliciesDeleteResp, error) {
	var (
		data PoliciesDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Policy is a sub type of PoliciesGetResp represeting information about an action group
type Policy struct {
	ID          string     `json:"_id,omitempty"`
	SeqNo       *int       `json:"_seq_no,omitempty"`
	PrimaryTerm *int       `json:"_primary_term,omitempty"`
	Policy      PolicyBody `json:"policy"`
}

// PolicyBody is a sub type of Policy containing information about the policy
type PolicyBody struct {
	PolicyID          string                   `json:"policy_id,omitempty"`
	Description       string                   `json:"description,omitempty"`
	LastUpdatedTime   int64                    `json:"last_updated_time,omitempty"`
	SchemaVersion     int                      `json:"schema_version,omitempty"`
	ErrorNotification *PolicyErrorNotification `json:"error_notification,omitempty"`
	DefaultState      string                   `json:"default_state"`
	States            []PolicyState            `json:"states"`
	Template          []Template               `json:"ism_template,omitempty"`
}

// PolicyErrorNotification is a sub type of PolicyBody containing information about error notification
type PolicyErrorNotification struct {
	Channel         *NotificationChannel        `json:"channel,omitempty"`
	Destination     *NotificationDestination    `json:"destination,omitempty"`
	MessageTemplate NotificationMessageTemplate `json:"message_template"`
}

// NotificationChannel is a sub type of PolicyErrorNotification containg the channel id
type NotificationChannel struct {
	ID string `json:"id"`
}

// NotificationDestination is a sub type of PolicyErrorNotification containing information about notification destinations
type NotificationDestination struct {
	Chime         *NotificationDestinationURL           `json:"chime,omitempty"`
	Slack         *NotificationDestinationURL           `json:"slack,omitempty"`
	CustomWebhook *NotificationDestinationCustomWebhook `json:"custom_webhook,omitempty"`
}

// NotificationDestinationURL is sub type of NotificationDestination containing the url of the notification destination
type NotificationDestinationURL struct {
	URL string `json:"url"`
}

// NotificationDestinationCustomWebhook is a sub type of NotificationDestination containing parameters for the custom webhook destination
type NotificationDestinationCustomWebhook struct {
	URL          string            `json:"url,omitempty"`
	HeaderParams map[string]string `json:"header_params,omitempty"`
	Host         string            `json:"host,omitempty"`
	Password     string            `json:"password,omitempty"`
	Path         string            `json:"path,omitempty"`
	Port         int               `json:"port,omitempty"`
	QueryParams  map[string]string `json:"query_params,omitempty"`
	Scheme       string            `json:"scheme,omitempty"`
	Username     string            `json:"username,omitempty"`
}

// NotificationMessageTemplate is a sub type of PolicyErrorNotification containing a pattern or string for the error message
type NotificationMessageTemplate struct {
	Source string `json:"source"`
	Lang   string `json:"lang,omitempty"`
}

// PolicyState uis a sub type of PolicyBody containing information about the policy state
type PolicyState struct {
	Name        string                   `json:"name"`
	Actions     []PolicyStateAction      `json:"actions,omitempty"`
	Transitions *[]PolicyStateTransition `json:"transitions,omitempty"`
}

// PolicyStateAction is a sub type of PolicyState containing all type of policy actions
type PolicyStateAction struct {
	Timeout       string                    `json:"timeout,omitempty"`
	Retry         *PolicyStateRetry         `json:"retry,omitempty"`
	ForceMerge    *PolicyStateForeMerge     `json:"force_merge,omitempty"`
	ReadOnly      *PolicyStateReadOnly      `json:"read_only,omitempty"`
	ReadWrite     *PolicyStateReadWrite     `json:"read_write,omitempty"`
	ReplicaCount  *PolicyStateReplicaCount  `json:"replica_count,omitempty"`
	Shrink        *PolicyStateShrink        `json:"shrink,omitempty"`
	Close         *PolicyStateClose         `json:"close,omitempty"`
	Open          *PolicyStateOpen          `json:"open,omitempty"`
	Delete        *PolicyStateDelete        `json:"delete,omitempty"`
	Rollover      *PolicyStateRollover      `json:"rollover,omitempty"`
	Notification  *PolicyStateNotification  `json:"notification,omitempty"`
	Snapshot      *PolicyStateSnapshot      `json:"snapshot,omitempty"`
	IndexPriority *PolicyStateIndexPriority `json:"index_priority,omitempty"`
	Allocation    *PolicyStateAllocation    `json:"allocation,omitempty"`
	Rollup        *PolicyStateRollup        `json:"rollup,omitempty"`
}

// Template is a sub type of PolicyBody containing information about the ims template
type Template struct {
	IndexPatterns   []string `json:"index_patterns,omitempty"`
	Priority        int      `json:"priority"`
	LastUpdatedTime int64    `json:"last_updated_time,omitempty"`
}

// PolicyStateRetry represents the retry action
type PolicyStateRetry struct {
	Count   int    `json:"count"`
	Backoff string `json:"backoff,omitempty"`
	Delay   string `json:"delay,omitempty"`
}

// PolicyStateForeMerge represents the force_merge action
type PolicyStateForeMerge struct {
	MaxNumSegments       int    `json:"max_num_segments"`
	WaitForCompletion    *bool  `json:"wait_for_completion,omitempty"`
	TaskExecutionTimeout string `json:"task_execution_timeout,omitempty"`
}

// PolicyStateReadOnly represents the read_only action
type PolicyStateReadOnly struct{}

// PolicyStateReadWrite represents the read_write action
type PolicyStateReadWrite struct{}

// PolicyStateReplicaCount represents the replica_count action
type PolicyStateReplicaCount struct {
	NumberOfReplicas int `json:"number_of_replicas"`
}

// PolicyStateShrink represents the Shrink action
type PolicyStateShrink struct {
	NumNewShards             int     `json:"num_new_shards,omitempty"`
	MaxShardSize             string  `json:"max_shard_size,omitempty"`
	PercentageOfSourceShards float32 `json:"percentage_of_source_shards,omitempty"`
	TargetIndexNameTemplate  *struct {
		Source string `json:"source"`
		Lang   string `json:"lang,omitempty"`
	} `json:"target_index_name_template,omitempty"`
	Aliases       []map[string]any `json:"aliases,omitempty"`
	SwitchAliases bool             `json:"switch_aliases,omitempty"`
	ForceUnsafe   bool             `json:"force_unsafe,omitempty"`
}

// PolicyStateClose represents the close action
type PolicyStateClose struct{}

// PolicyStateOpen represents the open action
type PolicyStateOpen struct{}

// PolicyStateDelete represents the delete action
type PolicyStateDelete struct{}

// PolicyStateRollover represents the rollover action
type PolicyStateRollover struct {
	MinSize             string `json:"min_size,omitempty"`
	MinPrimaryShardSize string `json:"min_primary_shard_size,omitempty"`
	MinDocCount         int    `json:"min_doc_count,omitempty"`
	MinIndexAge         string `json:"min_index_age,omitempty"`
	CopyAlias           bool   `json:"copy_alias,omitempty"`
}

// PolicyStateNotification represents the notification action
type PolicyStateNotification struct {
	Destination     NotificationDestination     `json:"destination"`
	MessageTemplate NotificationMessageTemplate `json:"message_template"`
}

// PolicyStateSnapshot represents the snapshot action
type PolicyStateSnapshot struct {
	Repository string `json:"repository"`
	Snapshot   string `json:"snapshot"`
}

// PolicyStateIndexPriority represents the index_priority action
type PolicyStateIndexPriority struct {
	Priority int `json:"priority"`
}

// PolicyStateAllocation represents the allocation action
type PolicyStateAllocation struct {
	Require map[string]string `json:"require,omitempty"`
	Include map[string]string `json:"include,omitempty"`
	Exclude map[string]string `json:"exclude,omitempty"`
	WaitFor *bool             `json:"wait_for,omitempty"`
}

// PolicyStateRollup represents the rollup action
type PolicyStateRollup struct {
	ISMRollup struct {
		Description string            `json:"description,omitempty"`
		TargetIndex string            `json:"target_index"`
		PageSize    string            `json:"page_size"`
		Dimensions  []json.RawMessage `json:"dimensions"`
		Metrics     []struct {
			SourceField string                `json:"source_field"`
			Metrics     []map[string]struct{} `json:"metrics"`
		} `json:"metrics"`
	} `json:"ism_rollup"`
}

// PolicyStateTransitionCondition is a sub type of PolicyStateTransition containing conditions for a transition
type PolicyStateTransitionCondition struct {
	MinIndexAge    string                              `json:"min_index_age,omitempty"`
	MinRolloverAge string                              `json:"min_rollover_age,omitempty"`
	MinDocCount    int                                 `json:"min_doc_count,omitempty"`
	MinSize        string                              `json:"min_size,omitempty"`
	Cron           *PolicyStateTransitionConditionCron `json:"cron,omitempty"`
}

// PolicyStateTransitionConditionCron is a sub type of PolicyStateTransitionCondition containing a cron expression and timezone
type PolicyStateTransitionConditionCron struct {
	Expression string `json:"expression"`
	Timezone   string `json:"timezone"`
}

// PolicyStateTransition is a sub type of PolicyState containing information about transition to other states
type PolicyStateTransition struct {
	StateName  string                          `json:"state_name"`
	Conditions *PolicyStateTransitionCondition `json:"conditions,omitempty"`
}
