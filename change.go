package distconf

// ChangeOp describes the type of configuration change.
type ChangeOp string

const (
	OpAdd    ChangeOp = "+"
	OpUpdate ChangeOp = "~"
	OpRemove ChangeOp = "-"
)

// ConfigurationChange represents a pending configuration change. The diff is
// for display only; the actual mutation happens by registering and
// activating a whole config generation at once.
type ConfigurationChange interface {
	Describe() (ChangeOp, string)
	Warnings() []string
}
