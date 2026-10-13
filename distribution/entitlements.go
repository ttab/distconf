package distribution

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/akedrou/textdiff"
	"github.com/ttab/distconf"
	distributionv1 "github.com/ttab/elephant-public-api/elephant/distribution/v1"
)

// EntitlementConfig declares one entitlement: a product tier whose content
// an organisation only sees once it has been granted the entitlement by
// name. The label is that name. The entitlement says what it hides,
// through its gates; who holds it is the grants, which are managed on the
// service and not in the configuration.
//
// Entitlements are evaluated when content is read or delivered, never
// stamped on what is stored, so changing a gate takes effect when the
// generation is activated and rewrites nothing. A document that matches the
// gates of several entitlements requires all of them.
type EntitlementConfig struct {
	Name        string       `hcl:"name,label"`
	Description string       `hcl:"description,optional"`
	Gates       []GateConfig `hcl:"gate,block"`
}

// GateConfig hides documents of one type, the label: all of them, or with
// a condition only those whose delivery field carries any of the values.
//
// The condition is DeliveryField and Values together. The field has to be
// declared on the gated type both as a keyword delivery field and as a
// facet of the same name with the same expression, because it is read from
// both - the delivery-field row on the hot paths, the facet in the daily
// views. The service refuses a gate where it is not, when the generation
// is registered.
type GateConfig struct {
	DocumentType  string   `hcl:"document_type,label"`
	DeliveryField string   `hcl:"delivery_field,optional"`
	Values        []string `hcl:"values,optional"`
}

// entitlementNamePattern is what the service accepts as an entitlement
// name, and what a grant has to name.
var entitlementNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// validateEntitlements checks what an entitlement declaration can be
// checked for on its own: the name, that it is declared once across the
// directory, that it gates something, and that each condition is whole.
//
// That a gated type is configured, and that a gate's field is declared on
// it as both a keyword delivery field and a facet, is checked by the
// service when the generation is registered. It is the authority on it,
// and a check that only exists in the CLI is a check a direct RPC caller
// skips.
func validateEntitlements(entitlements []EntitlementConfig) error {
	// HCL only rejects duplicate labels within one file, so names
	// declared in two files have to be caught after the merge.
	seen := make(map[string]bool, len(entitlements))

	for _, e := range entitlements {
		if !entitlementNamePattern.MatchString(e.Name) {
			return fmt.Errorf(
				"entitlement %q: the name must match %s, it is what a grant names",
				e.Name, entitlementNamePattern.String())
		}

		if seen[e.Name] {
			return fmt.Errorf(
				"entitlement %q declared more than once", e.Name)
		}

		seen[e.Name] = true

		if len(e.Gates) == 0 {
			return fmt.Errorf(
				"entitlement %q has no gate block, so it would hide nothing",
				e.Name)
		}

		gated := make(map[string]bool, len(e.Gates))

		for _, g := range e.Gates {
			err := validateGate(e.Name, g)
			if err != nil {
				return err
			}

			if gated[g.DocumentType] {
				return fmt.Errorf(
					"entitlement %q gates %q more than once, put the values in one gate",
					e.Name, g.DocumentType)
			}

			gated[g.DocumentType] = true
		}
	}

	return nil
}

func validateGate(entitlement string, g GateConfig) error {
	if g.DocumentType == "" {
		return fmt.Errorf(
			"entitlement %q: a gate has no document type", entitlement)
	}

	switch {
	case g.DeliveryField == "" && len(g.Values) > 0:
		return fmt.Errorf(
			"entitlement %q: the gate on %q has values but no delivery_field to match them against",
			entitlement, g.DocumentType)
	case g.DeliveryField != "" && len(g.Values) == 0:
		// Read literally, a condition with no values matches nothing,
		// which is the opposite of what leaving the condition out
		// means. Neither reading is safe to guess.
		return fmt.Errorf(
			"entitlement %q: the gate on %q names the delivery_field %q but no values,"+
				" leave both out to gate the whole type",
			entitlement, g.DocumentType, g.DeliveryField)
	}

	if slices.Contains(g.Values, "") {
		return fmt.Errorf(
			"entitlement %q: the gate on %q has an empty value",
			entitlement, g.DocumentType)
	}

	return nil
}

// EntitlementSpec is an entitlement in a canonical shape: gates sorted by
// document type and values sorted and deduplicated. It is what the plan
// diffs and what is sent.
//
// The canonical order is not cosmetic. Neither the order of the gates nor
// the order of the values means anything - a document several gates match
// requires all of them, and a condition matches any of its values - so
// reordering them in the configuration must not read as a change, or every
// apply would register a new generation.
type EntitlementSpec struct {
	Name        string     `json:"-"`
	Description string     `json:"description,omitempty"`
	Gates       []GateSpec `json:"gates"`
}

// GateSpec is one gate of an EntitlementSpec.
type GateSpec struct {
	DocumentType  string   `json:"document_type"`
	DeliveryField string   `json:"delivery_field,omitempty"`
	Values        []string `json:"values,omitempty"`
}

// entitlementSpecs canonicalizes the configured entitlements, sorted by
// name - the order they are declared in, across files, means nothing
// either.
func entitlementSpecs(entitlements []EntitlementConfig) []EntitlementSpec {
	if len(entitlements) == 0 {
		return nil
	}

	out := make([]EntitlementSpec, len(entitlements))

	for i, e := range entitlements {
		spec := EntitlementSpec{
			Name:        e.Name,
			Description: e.Description,
			Gates:       make([]GateSpec, len(e.Gates)),
		}

		for j, g := range e.Gates {
			spec.Gates[j] = GateSpec(g)
		}

		out[i] = canonicalEntitlement(spec)
	}

	slices.SortFunc(out, func(a, b EntitlementSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return out
}

// canonicalEntitlement sorts the gates and their values. It is applied to
// both sides of the diff, so that what the active generation stores is
// compared in the same shape whatever order it was registered in.
func canonicalEntitlement(spec EntitlementSpec) EntitlementSpec {
	gates := make([]GateSpec, len(spec.Gates))

	for i, g := range spec.Gates {
		var values []string

		if len(g.Values) > 0 {
			values = slices.Clone(g.Values)
			slices.Sort(values)
			values = slices.Compact(values)
		}

		gates[i] = GateSpec{
			DocumentType:  g.DocumentType,
			DeliveryField: g.DeliveryField,
			Values:        values,
		}
	}

	slices.SortFunc(gates, func(a, b GateSpec) int {
		return strings.Compare(a.DocumentType, b.DocumentType)
	})

	spec.Gates = gates

	return spec
}

// entitlementsToRPC converts the canonical entitlements into the API
// representation. Nil for none, so that a configuration without
// entitlements sends what a generation without them reads back as.
func entitlementsToRPC(specs []EntitlementSpec) []*distributionv1.Entitlement {
	if len(specs) == 0 {
		return nil
	}

	out := make([]*distributionv1.Entitlement, len(specs))

	for i, spec := range specs {
		msg := distributionv1.Entitlement{
			Name:        spec.Name,
			Description: spec.Description,
			Gates: make(
				[]*distributionv1.EntitlementGate, len(spec.Gates)),
		}

		for j, g := range spec.Gates {
			gate := distributionv1.EntitlementGate{
				DocumentType: g.DocumentType,
			}

			if g.DeliveryField != "" {
				gate.Condition = &distributionv1.GateCondition{
					DeliveryField: g.DeliveryField,
					Values:        g.Values,
				}
			}

			msg.Gates[j] = &gate
		}

		out[i] = &msg
	}

	return out
}

// entitlementFromRPC reads an entitlement of the active generation into
// the canonical shape, entitlementsToRPC's inverse.
func entitlementFromRPC(msg *distributionv1.Entitlement) EntitlementSpec {
	spec := EntitlementSpec{
		Name:        msg.GetName(),
		Description: msg.GetDescription(),
		Gates:       make([]GateSpec, len(msg.GetGates())),
	}

	for i, g := range msg.GetGates() {
		spec.Gates[i] = GateSpec{
			DocumentType:  g.GetDocumentType(),
			DeliveryField: g.GetCondition().GetDeliveryField(),
			Values:        g.GetCondition().GetValues(),
		}
	}

	return canonicalEntitlement(spec)
}

func entitlementSpecJSON(spec EntitlementSpec) (string, error) {
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}

	return string(data), nil
}

// planEntitlements diffs the desired entitlements against the active
// generation's, one change per entitlement name.
func planEntitlements(
	desired []EntitlementSpec,
	active []*distributionv1.Entitlement,
) ([]distconf.ConfigurationChange, error) {
	activeByName := make(map[string]EntitlementSpec, len(active))

	for _, msg := range active {
		activeByName[msg.GetName()] = entitlementFromRPC(msg)
	}

	seen := make(map[string]bool, len(desired))

	var changes []distconf.ConfigurationChange

	for _, spec := range desired {
		seen[spec.Name] = true

		wanted, err := entitlementSpecJSON(spec)
		if err != nil {
			return nil, fmt.Errorf(
				"render entitlement %q: %w", spec.Name, err)
		}

		curr, ok := activeByName[spec.Name]
		if !ok {
			changes = append(changes, &entitlementPlanChange{
				name:   spec.Name,
				wanted: wanted,
				op:     distconf.OpAdd,
			})

			continue
		}

		current, err := entitlementSpecJSON(curr)
		if err != nil {
			return nil, fmt.Errorf(
				"render active entitlement %q: %w", spec.Name, err)
		}

		if current == wanted {
			continue
		}

		changes = append(changes, &entitlementPlanChange{
			name:    spec.Name,
			current: current,
			wanted:  wanted,
			op:      distconf.OpUpdate,
		})
	}

	// Sorted so that the removals print in a stable order: the map is
	// not.
	var removed []string

	for name := range activeByName {
		if !seen[name] {
			removed = append(removed, name)
		}
	}

	slices.Sort(removed)

	for _, name := range removed {
		changes = append(changes, &entitlementPlanChange{
			name: name,
			op:   distconf.OpRemove,
		})
	}

	return changes, nil
}

type entitlementPlanChange struct {
	name    string
	current string
	wanted  string
	op      distconf.ChangeOp
}

func (c *entitlementPlanChange) Describe() (distconf.ChangeOp, string) {
	switch c.op {
	case distconf.OpAdd:
		return distconf.OpAdd, fmt.Sprintf(
			"define entitlement %q:\n%s", c.name, c.wanted)
	case distconf.OpRemove:
		return distconf.OpRemove, fmt.Sprintf(
			"remove entitlement %q", c.name)
	case distconf.OpUpdate:
	}

	diff := textdiff.Unified("current", "wanted", c.current, c.wanted)

	return distconf.OpUpdate, fmt.Sprintf(
		"update entitlement %q:\n%s",
		c.name, strings.TrimRight(diff, "\n"))
}

// Warnings says what removing an entitlement does and does not do, since
// the plan line alone reads as if it took the grants with it.
func (c *entitlementPlanChange) Warnings() []string {
	if c.op != distconf.OpRemove {
		return nil
	}

	return []string{fmt.Sprintf(
		"removing entitlement %q lifts its gates: what they hid is"+
			" visible to callers without a grant, unless another"+
			" entitlement gates it. Its grants are kept and do"+
			" nothing until a generation defines it again", c.name)}
}
