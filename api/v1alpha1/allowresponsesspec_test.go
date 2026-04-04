package v1alpha1

import (
	"encoding/json"
	"testing"
)

func TestAllowResponsesSpecUnmarshalBoolTrue(t *testing.T) {
	var a AllowResponsesSpec
	if err := json.Unmarshal([]byte(`true`), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.ShouldEmit() {
		t.Error("expected ShouldEmit()=true for boolean true")
	}
	if a.MaxMsgs != nil || a.TTL != nil {
		t.Error("expected nil MaxMsgs/TTL for boolean form")
	}
}

func TestAllowResponsesSpecUnmarshalBoolFalse(t *testing.T) {
	var a AllowResponsesSpec
	if err := json.Unmarshal([]byte(`false`), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ShouldEmit() {
		t.Error("expected ShouldEmit()=false for boolean false")
	}
}

func TestAllowResponsesSpecUnmarshalEmptyObject(t *testing.T) {
	var a AllowResponsesSpec
	if err := json.Unmarshal([]byte(`{}`), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.ShouldEmit() {
		t.Error("expected ShouldEmit()=true for empty object form")
	}
	if a.MaxMsgs != nil || a.TTL != nil {
		t.Error("expected nil MaxMsgs/TTL for empty object form")
	}
}

func TestAllowResponsesSpecUnmarshalObjectWithFields(t *testing.T) {
	var a AllowResponsesSpec
	if err := json.Unmarshal([]byte(`{"maxMsgs":5,"ttl":"1m"}`), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.ShouldEmit() {
		t.Error("expected ShouldEmit()=true for object form")
	}
	if a.MaxMsgs == nil || *a.MaxMsgs != 5 {
		t.Errorf("expected MaxMsgs=5, got %v", a.MaxMsgs)
	}
	if a.TTL == nil || *a.TTL != "1m" {
		t.Errorf("expected TTL=1m, got %v", a.TTL)
	}
}

func TestAllowResponsesSpecMarshalBoolTrue(t *testing.T) {
	a := NewAllowResponsesBool(true)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "true" {
		t.Errorf("expected 'true', got %s", data)
	}
}

func TestAllowResponsesSpecMarshalBoolFalse(t *testing.T) {
	a := NewAllowResponsesBool(false)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "false" {
		t.Errorf("expected 'false', got %s", data)
	}
}

func TestAllowResponsesSpecMarshalObjectForm(t *testing.T) {
	maxMsgs := 3
	ttl := "30s"
	a := &AllowResponsesSpec{MaxMsgs: &maxMsgs, TTL: &ttl}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got AllowResponsesSpec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("round-trip unmarshal error: %v", err)
	}
	if got.MaxMsgs == nil || *got.MaxMsgs != 3 {
		t.Errorf("expected MaxMsgs=3, got %v", got.MaxMsgs)
	}
	if got.TTL == nil || *got.TTL != "30s" {
		t.Errorf("expected TTL=30s, got %v", got.TTL)
	}
}

func TestAllowResponsesSpecDeepCopy(t *testing.T) {
	orig := NewAllowResponsesBool(true)
	cp := orig.DeepCopy()
	if cp == orig {
		t.Error("DeepCopy should return a new pointer")
	}
	if !cp.ShouldEmit() {
		t.Error("expected ShouldEmit()=true on deep copy")
	}
}

func TestNewAllowResponsesBool(t *testing.T) {
	trueSpec := NewAllowResponsesBool(true)
	if !trueSpec.ShouldEmit() {
		t.Error("expected true")
	}
	falseSpec := NewAllowResponsesBool(false)
	if falseSpec.ShouldEmit() {
		t.Error("expected false")
	}
}
