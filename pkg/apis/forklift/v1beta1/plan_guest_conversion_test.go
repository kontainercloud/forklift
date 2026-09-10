package v1beta1

import (
	"testing"

	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/plan"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
)

func TestRequiresGuestConversion(t *testing.T) {
	newPlan := func(sourceType ProviderType, nutanixGuestConversion bool) *Plan {
		return &Plan{
			Spec: PlanSpec{
				NutanixGuestConversion: nutanixGuestConversion,
			},
			Referenced: Referenced{
				Provider: struct {
					Source, Destination *Provider
				}{
					Source: &Provider{Spec: ProviderSpec{Type: &sourceType}},
				},
			},
		}
	}

	cases := []struct {
		name       string
		sourceType ProviderType
		optIn      bool
		want       bool
	}{
		{"vSphere always requires conversion", VSphere, false, true},
		{"Ova always requires conversion", Ova, false, true},
		{"HyperV always requires conversion", HyperV, false, true},
		{"EC2 always requires conversion", EC2, false, true},
		{"Azure always requires conversion", Azure, false, true},
		{"Nutanix defaults to no conversion", Nutanix, false, false},
		{"Nutanix opts in via NutanixGuestConversion", Nutanix, true, true},
		{"oVirt never requires conversion", OVirt, false, false},
		{"OpenStack never requires conversion", OpenStack, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := newPlan(c.sourceType, c.optIn).RequiresGuestConversion()
			if got != c.want {
				t.Errorf("RequiresGuestConversion() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequiresGuestConversion_NilSource(t *testing.T) {
	p := &Plan{}
	if p.RequiresGuestConversion() {
		t.Error("expected false when source provider is nil")
	}
}

// TestShouldUseV2vForTransfer_NutanixAlwaysFalse guards the design
// decision behind Nutanix's guest-conversion support (Tier 3 in
// docs/enhancements/nutanix-ahv-migration-maturity.md): even when
// NutanixGuestConversion opts a plan into running the conversion pod, it
// must run in virt-v2v-in-place mode against the already-CDI-imported
// disk, never in virt-v2v-does-the-transfer mode -- Nutanix's real image
// endpoint doesn't support the HTTP Range requests that mode would need.
func TestShouldUseV2vForTransfer_NutanixAlwaysFalse(t *testing.T) {
	sourceType, destType := Nutanix, OpenShift
	p := &Plan{
		Spec: PlanSpec{
			NutanixGuestConversion: true,
			VMs:                    []plan.VM{{Ref: ref.Ref{ID: "vm-1"}}},
		},
		Referenced: Referenced{
			Provider: struct {
				Source, Destination *Provider
			}{
				Source:      &Provider{Spec: ProviderSpec{Type: &sourceType, URL: "https://pc"}},
				Destination: &Provider{Spec: ProviderSpec{Type: &destType}},
			},
		},
	}
	ok, err := p.ShouldUseV2vForTransfer(ref.Ref{ID: "vm-1"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ShouldUseV2vForTransfer to stay false for Nutanix even with NutanixGuestConversion set")
	}
}
