package model

import "time"

// Role 是后台角色。key 是稳定标识，创建后不可修改；权限通过 role_permissions 分配。
// 系统角色（admin）隐式拥有全部权限，不可删除、不可改权限。
type Role struct {
	Key         string    `gorm:"column:role_key;type:varchar(64);primaryKey;comment:角色标识，小写点号分隔，创建后不可改"`
	Name        string    `gorm:"type:varchar(64);not null;comment:角色显示名，可修改"`
	Description string    `gorm:"type:varchar(200);not null;default:'';comment:角色说明，仅展示"`
	IsSystem    bool      `gorm:"not null;default:false;comment:是否系统角色，系统角色隐式拥有全部权限、不可删除、不可改权限"`
	CreatedAt   time.Time `gorm:"comment:创建时间"`
	UpdatedAt   time.Time `gorm:"comment:最近更新时间"`
}

func (Role) TableName() string { return "roles" }

// Permission 是代码注册表在库里的投影，只由启动同步维护：
// 新增插入、显示名/模块/排序更新，代码中已消失的 key 只置 deprecated_at，绝不删除。
type Permission struct {
	Key          string     `gorm:"column:permission_key;type:varchar(64);primaryKey;comment:权限点标识，形如 module.action"`
	Module       string     `gorm:"type:varchar(32);not null;comment:所属模块，用于后台分组展示"`
	Name         string     `gorm:"type:varchar(64);not null;comment:权限点显示名"`
	Description  string     `gorm:"type:varchar(200);not null;default:'';comment:权限点说明，仅展示"`
	Sort         int        `gorm:"not null;default:0;comment:展示排序，越小越靠前"`
	DeprecatedAt *time.Time `gorm:"comment:废弃时间，代码中已移除的 key 只标废弃不删除"`
	CreatedAt    time.Time  `gorm:"comment:创建时间"`
}

func (Permission) TableName() string { return "permissions" }

// RolePermission 是角色与权限点的分配关系，也是后台界面唯一可配置的授权数据。
type RolePermission struct {
	RoleKey       string    `gorm:"type:varchar(64);primaryKey;comment:角色标识，指向 roles.key"`
	PermissionKey string    `gorm:"type:varchar(64);primaryKey;comment:权限点标识，指向 permissions.key"`
	CreatedAt     time.Time `gorm:"comment:分配时间"`
}

func (RolePermission) TableName() string { return "role_permissions" }
