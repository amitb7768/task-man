package fsm

import (
	"time"

	"gorm.io/datatypes"
)

// StateHistory represents a single state transition record in the database.
// This is used for audit trails and tracking the history of state changes.
type StateHistory struct {
	ID            uint64         `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	ResourceID    string         `gorm:"not null;column:resource_id" json:"resourceId"`
	ResourceType  string         `gorm:"not null;column:resource_type" json:"resourceType"`
	FromStatus    *string        `gorm:"column:from_status" json:"fromStatus,omitempty"`
	FromSubstatus *string        `gorm:"column:from_substatus" json:"fromSubstatus,omitempty"`
	ToStatus      string         `gorm:"not null;column:to_status" json:"toStatus"`
	ToSubstatus   string         `gorm:"not null;column:to_substatus" json:"toSubstatus"`
	Event         string         `gorm:"not null;column:event" json:"event"`
	ActorID       string         `gorm:"not null;column:actor_id" json:"actorId"`
	Metadata      datatypes.JSON `gorm:"column:metadata" json:"metadata,omitempty"`
	CreatedAt     time.Time      `gorm:"not null;column:created_at;autoCreateTime" json:"createdAt"`
}

// TableName specifies the table name for the StateHistory model
func (StateHistory) TableName() string {
	return "state_history"
}
