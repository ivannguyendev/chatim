package authz

var Required = map[string]Permission{
	"CreateRole":          "iam:roles.write",
	"UpdateRole":          "iam:roles.write",
	"DeleteRole":          "iam:roles.write",
	"GetRole":             "iam:roles.read",
	"ListRoles":           "iam:roles.read",
	"AssignAccountRole":   "iam:roles.assign",
	"UnassignAccountRole": "iam:roles.assign",
	"AssignGroupRole":     "iam:roles.assign",
	"UnassignGroupRole":   "iam:roles.assign",
	"CreateGroup":         "iam:groups.write",
	"UpdateGroup":         "iam:groups.write",
	"DeleteGroup":         "iam:groups.write",
	"ListGroups":          "iam:groups.read",
	"SetAccountGroups":    "iam:groups.members.write",
	"GetAccount":          "iam:accounts.read",
	"ListAccounts":        "iam:accounts.read",
}
