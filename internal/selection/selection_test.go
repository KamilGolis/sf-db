package selection

import (
	"fmt"
	"reflect"
	"testing"
)

func infos() []Info {
	return []Info{
		{Name: "Account", Queryable: true},
		{Name: "AccountHistory", Queryable: true},
		{Name: "AccountShare", Queryable: true},
		{Name: "MyObject__c", Custom: true, Queryable: true},
		{Name: "Setting__c", Custom: true, Queryable: true},
		{Name: "NotQueryable", Queryable: false},
		{Name: "OldObject", Queryable: true, DeprecatedAndHidden: true},
	}
}

func TestExplicitOnlySortsAndDeduplicates(t *testing.T) {
	got, err := Resolve(nil, Options{Explicit: []string{"Contact", "Account", "contact"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Account", "Contact"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAllExcludesSystemAndNonQueryable(t *testing.T) {
	got, err := Resolve(infos(), Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Account", "MyObject__c", "Setting__c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestIncludeSystem(t *testing.T) {
	got, err := Resolve(infos(), Options{All: true, IncludeSystem: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Account", "AccountHistory", "AccountShare", "MyObject__c", "Setting__c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCustomAndStandardCombine(t *testing.T) {
	got, err := Resolve(infos(), Options{Custom: true, Standard: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Account", "MyObject__c", "Setting__c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExplicitUnionsWithSelector(t *testing.T) {
	got, err := Resolve(infos(), Options{Explicit: []string{"Account"}, Custom: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Account", "MyObject__c", "Setting__c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAllConflictsWithCategory(t *testing.T) {
	if _, err := Resolve(infos(), Options{All: true, Custom: true}); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestRequiresASource(t *testing.T) {
	if _, err := Resolve(nil, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSelectorRequiresObjectList(t *testing.T) {
	if _, err := Resolve(nil, Options{Custom: true}); err == nil {
		t.Fatal("expected error")
	}
}

func TestOrgCounts(t *testing.T) {
	infos := make([]Info, 0, 1229)
	for i := 0; i < 16; i++ {
		infos = append(infos, Info{Name: fmt.Sprintf("Custom%02d__c", i), Custom: true, Queryable: true})
	}
	for i := 0; i < 3; i++ {
		infos = append(infos, Info{Name: fmt.Sprintf("HiddenCustom%02d__c", i), Custom: true, Queryable: false})
	}
	for i := 0; i < 705; i++ {
		infos = append(infos, Info{Name: fmt.Sprintf("Standard%03d", i), Queryable: true})
	}
	for i := 0; i < 321; i++ {
		infos = append(infos, Info{Name: fmt.Sprintf("Standard%03dHistory", i), Queryable: true})
	}
	for i := 0; i < 184; i++ {
		infos = append(infos, Info{Name: fmt.Sprintf("HiddenStandard%03d", i), Queryable: false})
	}

	tests := []struct {
		name string
		opts Options
		want int
	}{
		{"all business", Options{All: true}, 721},
		{"standard business", Options{Standard: true}, 705},
		{"custom", Options{Custom: true}, 16},
		{"all with system", Options{All: true, IncludeSystem: true}, 1042},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(infos, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Fatalf("len = %d, want %d", len(got), tt.want)
			}
		})
	}
}
