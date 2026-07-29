package distconf

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ttab/eleconf"
	"github.com/ttab/revisor"
)

// LoadedSchema is a re-export of eleconf.LoadedSchema for convenience.
type LoadedSchema = eleconf.LoadedSchema

// LockFilePath returns the path to the schema lock file in the given
// directory.
func LockFilePath(dir string) string {
	return eleconf.LockFilePath(dir)
}

// LoadLockFile loads a schema lockfile from disk.
func LoadLockFile(fileName string) (*eleconf.SchemaLockfile, error) {
	lf, err := eleconf.LoadLockFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("load lock file: %w", err)
	}

	return lf, nil
}

// NewSchemaLockFile creates a new lockfile from loaded schemas.
func NewSchemaLockFile(loaded []LoadedSchema) *eleconf.SchemaLockfile {
	return eleconf.NewSchemaLockFile(loaded, nil)
}

// LoadSchemaSet loads the schemas in a schema set, validating against the
// lockfile.
func LoadSchemaSet(
	ctx context.Context,
	set eleconf.SchemaSet,
	lockfile *eleconf.SchemaLockfile,
	init bool,
) ([]LoadedSchema, error) {
	schemas, err := eleconf.LoadSchemaSet(ctx, set, lockfile, init)
	if err != nil {
		return nil, fmt.Errorf("load schema set: %w", err)
	}

	return schemas, nil
}

// SchemaRef is a service-neutral reference to a schema in a configuration
// generation.
type SchemaRef struct {
	Name    string
	Version string
	Spec    string
}

// PlanSchemas compares the desired loaded schemas against the schemas of
// the active generation and returns the desired schema references
// together with the changes needed to get there.
func PlanSchemas(
	want []LoadedSchema, active []SchemaRef,
) ([]SchemaRef, []ConfigurationChange) {
	current := make(map[string]SchemaRef, len(active))
	for _, sc := range active {
		current[sc.Name] = sc
	}

	desired := make([]SchemaRef, 0, len(want))
	seen := make(map[string]bool, len(want))

	var changes []ConfigurationChange

	for _, sc := range want {
		seen[sc.Lock.Name] = true

		desired = append(desired, SchemaRef{
			Name:    sc.Lock.Name,
			Version: sc.Lock.Version,
			Spec:    string(sc.Data),
		})

		curr, ok := current[sc.Lock.Name]
		switch {
		case !ok:
			changes = append(changes, &schemaPlanChange{
				op: OpAdd,
				message: fmt.Sprintf(
					"add schema %s@%s",
					sc.Lock.Name, sc.Lock.Version),
			})
		case curr.Version != sc.Lock.Version:
			changes = append(changes, &schemaPlanChange{
				op: OpUpdate,
				message: fmt.Sprintf(
					"upgrade schema %s %s => %s",
					sc.Lock.Name, curr.Version, sc.Lock.Version),
			})
		}
	}

	// Anything in the active gen not in the desired set is being removed.
	for _, sc := range active {
		if seen[sc.Name] {
			continue
		}

		changes = append(changes, &schemaPlanChange{
			op: OpRemove,
			message: fmt.Sprintf(
				"remove schema %s@%s", sc.Name, sc.Version),
		})
	}

	return desired, changes
}

type schemaPlanChange struct {
	op      ChangeOp
	message string
}

func (c *schemaPlanChange) Describe() (ChangeOp, string) {
	return c.op, c.message
}

func (c *schemaPlanChange) Warnings() []string {
	return nil
}

// DeclaredTypes returns the set of document types declared by the given
// schemas.
func DeclaredTypes(schemas []LoadedSchema) (map[string]bool, error) {
	types := make(map[string]bool)

	for _, sc := range schemas {
		var spec revisor.ConstraintSet

		err := json.Unmarshal(sc.Data, &spec)
		if err != nil {
			return nil, fmt.Errorf(
				"unmarshal schema %q: %w", sc.Lock.Name, err)
		}

		for _, doc := range spec.Documents {
			if doc.Declares == "" {
				continue
			}

			types[doc.Declares] = true
		}
	}

	return types, nil
}

// ValidateDeclaredTypes verifies that every one of the given document
// types is declared by one of the loaded schemas.
func ValidateDeclaredTypes(types []string, schemas []LoadedSchema) error {
	declared, err := DeclaredTypes(schemas)
	if err != nil {
		return err
	}

	var undeclared []string

	for _, t := range types {
		if declared[t] {
			continue
		}

		undeclared = append(undeclared, t)
	}

	if len(undeclared) == 0 {
		return nil
	}

	sort.Strings(undeclared)

	quoted := make([]string, len(undeclared))
	for i, t := range undeclared {
		quoted[i] = fmt.Sprintf("%q", t)
	}

	return fmt.Errorf(
		"undeclared document types: %s (not declared in any schema)",
		strings.Join(quoted, ", "))
}
