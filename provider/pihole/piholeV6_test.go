/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pihole

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

type testPiholeClientV6 struct {
	endpoints []*endpoint.Endpoint
	requests  *requestTrackerV6
	trigger   string
}

func (t *testPiholeClientV6) listRecords(_ context.Context, rtype string) ([]*endpoint.Endpoint, error) {
	out := make([]*endpoint.Endpoint, 0)
	if t.trigger == "AERROR" {
		return nil, errors.New("AERROR")
	}
	if t.trigger == "AAAAERROR" {
		return nil, errors.New("AAAAERROR")
	}
	if t.trigger == "CNAMEERROR" {
		return nil, errors.New("CNAMEERROR")
	}
	for _, ep := range t.endpoints {
		if ep.RecordType == rtype {
			out = append(out, ep)
		}
	}
	return out, nil
}

func (t *testPiholeClientV6) createRecord(_ context.Context, ep *endpoint.Endpoint) error {
	t.endpoints = append(t.endpoints, ep)
	t.requests.createRequests = append(t.requests.createRequests, ep)
	return nil
}

func (t *testPiholeClientV6) deleteRecord(_ context.Context, ep *endpoint.Endpoint) error {
	newEPs := make([]*endpoint.Endpoint, 0)
	for _, existing := range t.endpoints {
		if existing.DNSName != ep.DNSName || cmp.Diff(existing.Targets, ep.Targets) != "" || existing.RecordType != ep.RecordType {
			newEPs = append(newEPs, existing)
		}
	}
	t.endpoints = newEPs
	t.requests.deleteRequests = append(t.requests.deleteRequests, ep)
	return nil
}

type requestTrackerV6 struct {
	createRequests []*endpoint.Endpoint
	deleteRequests []*endpoint.Endpoint
}

func (r *requestTrackerV6) clear() {
	r.createRequests = nil
	r.deleteRequests = nil
}

func TestErrorHandling(t *testing.T) {
	requests := requestTrackerV6{}
	p := &PiholeProvider{
		api:        &testPiholeClientV6{endpoints: make([]*endpoint.Endpoint, 0), requests: &requests},
		apiVersion: "6",
	}

	p.api.(*testPiholeClientV6).trigger = "AERROR"
	_, err := p.Records(context.Background())
	if err.Error() != "AERROR" {
		t.Fatal(err)
	}

	p.api.(*testPiholeClientV6).trigger = "AAAAERROR"
	_, err = p.Records(context.Background())
	if err.Error() != "AAAAERROR" {
		t.Fatal(err)
	}

	p.api.(*testPiholeClientV6).trigger = "CNAMEERROR"
	_, err = p.Records(context.Background())
	if err.Error() != "CNAMEERROR" {
		t.Fatal(err)
	}

}

func TestNewPiholeProviderV6(t *testing.T) {
	// Test invalid configuration
	_, err := NewPiholeProvider(PiholeConfig{APIVersion: "7"})
	if err == nil {
		t.Error("Expected error from invalid configuration")
	}
	// Test valid configuration
	_, err = NewPiholeProvider(PiholeConfig{Server: "test.example.com", APIVersion: "6"})
	if err != nil {
		t.Error("Expected no error from valid configuration, got:", err)
	}
}

func TestProviderV6(t *testing.T) {
	requests := requestTrackerV6{}
	p := &PiholeProvider{
		api:        &testPiholeClientV6{endpoints: make([]*endpoint.Endpoint, 0), requests: &requests},
		apiVersion: "6",
	}

	records, err := p.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatal("Expected empty list of records, got:", records)
	}

	// Populate the provider with records
	records = []*endpoint.Endpoint{
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"192.168.1.1"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"192.168.1.2"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test3.example.com",
			Targets:    []string{"192.168.1.3"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"fc00::1:192:168:1:1"},
			RecordType: endpoint.RecordTypeAAAA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"fc00::1:192:168:1:2"},
			RecordType: endpoint.RecordTypeAAAA,
		},
		{
			DNSName:    "test3.example.com",
			Targets:    []string{"fc00::1:192:168:1:3"},
			RecordType: endpoint.RecordTypeAAAA,
		},
	}
	if err := p.ApplyChanges(context.Background(), &plan.Changes{
		Create: records,
	}); err != nil {
		t.Fatal(err)
	}

	// Test records are correct on retrieval

	newRecords, err := p.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(newRecords) != 6 {
		t.Fatal("Expected list of 6 records, got:", records)
	}
	if len(requests.createRequests) != 6 {
		t.Fatal("Expected 6 create requests, got:", requests.createRequests)
	}
	if len(requests.deleteRequests) != 0 {
		t.Fatal("Expected no delete requests, got:", requests.deleteRequests)
	}

	for idx, record := range records {
		if newRecords[idx].DNSName != record.DNSName {
			t.Error("DNS Name malformed on retrieval, got:", newRecords[idx].DNSName, "expected:", record.DNSName)
		}
		if newRecords[idx].Targets[0] != record.Targets[0] {
			t.Error("Targets malformed on retrieval, got:", newRecords[idx].Targets, "expected:", record.Targets)
		}

		if !reflect.DeepEqual(requests.createRequests[idx], record) {
			t.Error("Unexpected create request, got:", newRecords[idx].DNSName, "expected:", record.DNSName)
		}
	}

	requests.clear()

	// Test delete a record

	records = []*endpoint.Endpoint{
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"192.168.1.1"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"192.168.1.2"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"fc00::1:192:168:1:1"},
			RecordType: endpoint.RecordTypeAAAA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"fc00::1:192:168:1:2"},
			RecordType: endpoint.RecordTypeAAAA,
		},
	}
	recordToDeleteA := endpoint.Endpoint{
		DNSName:    "test3.example.com",
		Targets:    []string{"192.168.1.3"},
		RecordType: endpoint.RecordTypeA,
	}
	if err := p.ApplyChanges(context.Background(), &plan.Changes{
		Delete: []*endpoint.Endpoint{
			&recordToDeleteA,
		},
	}); err != nil {
		t.Fatal(err)
	}
	recordToDeleteAAAA := endpoint.Endpoint{
		DNSName:    "test3.example.com",
		Targets:    []string{"fc00::1:192:168:1:3"},
		RecordType: endpoint.RecordTypeAAAA,
	}
	if err := p.ApplyChanges(context.Background(), &plan.Changes{
		Delete: []*endpoint.Endpoint{
			&recordToDeleteAAAA,
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Test records are updated
	newRecords, err = p.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(newRecords) != 4 {
		t.Fatal("Expected list of 4 records, got:", records)
	}
	if len(requests.createRequests) != 0 {
		t.Fatal("Expected no create requests, got:", requests.createRequests)
	}
	if len(requests.deleteRequests) != 2 {
		t.Fatal("Expected 2 delete request, got:", requests.deleteRequests)
	}

	for idx, record := range records {
		if newRecords[idx].DNSName != record.DNSName {
			t.Error("DNS Name malformed on retrieval, got:", newRecords[idx].DNSName, "expected:", record.DNSName)
		}
		if newRecords[idx].Targets[0] != record.Targets[0] {
			t.Error("Targets malformed on retrieval, got:", newRecords[idx].Targets, "expected:", record.Targets)
		}
	}

	if !reflect.DeepEqual(requests.deleteRequests[0], &recordToDeleteA) {
		t.Error("Unexpected delete request, got:", requests.deleteRequests[0], "expected:", recordToDeleteA)
	}
	if !reflect.DeepEqual(requests.deleteRequests[1], &recordToDeleteAAAA) {
		t.Error("Unexpected delete request, got:", requests.deleteRequests[1], "expected:", recordToDeleteAAAA)
	}

	requests.clear()

	// Test update a record

	records = []*endpoint.Endpoint{
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"192.168.1.1"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"10.0.0.1"},
			RecordType: endpoint.RecordTypeA,
		},
		{
			DNSName:    "test1.example.com",
			Targets:    []string{"fc00::1:192:168:1:1"},
			RecordType: endpoint.RecordTypeAAAA,
		},
		{
			DNSName:    "test2.example.com",
			Targets:    []string{"fc00::1:10:0:0:1"},
			RecordType: endpoint.RecordTypeAAAA,
		},
	}
	if err := p.ApplyChanges(context.Background(), &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "test1.example.com",
				Targets:    []string{"192.168.1.1"},
				RecordType: endpoint.RecordTypeA,
			},
			{
				DNSName:    "test2.example.com",
				Targets:    []string{"192.168.1.2"},
				RecordType: endpoint.RecordTypeA,
			},
			{
				DNSName:    "test1.example.com",
				Targets:    []string{"fc00::1:192:168:1:1"},
				RecordType: endpoint.RecordTypeAAAA,
			},
			{
				DNSName:    "test2.example.com",
				Targets:    []string{"fc00::1:192:168:1:2"},
				RecordType: endpoint.RecordTypeAAAA,
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "test1.example.com",
				Targets:    []string{"192.168.1.1"},
				RecordType: endpoint.RecordTypeA,
			},
			{
				DNSName:    "test2.example.com",
				Targets:    []string{"10.0.0.1"},
				RecordType: endpoint.RecordTypeA,
			},
			{
				DNSName:    "test2.example.com",
				Targets:    []string{"10.0.0.2"},
				RecordType: endpoint.RecordTypeA,
			},
			{
				DNSName:    "test1.example.com",
				Targets:    []string{"fc00::1:192:168:1:1"},
				RecordType: endpoint.RecordTypeAAAA,
			},
			{
				DNSName:    "test2.example.com",
				Targets:    []string{"fc00::1:10:0:0:1"},
				RecordType: endpoint.RecordTypeAAAA,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Test records are updated
	newRecords, err = p.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(newRecords) != 4 {
		t.Fatal("Expected list of 4 records, got:", newRecords)
	}
	if len(requests.createRequests) != 2 {
		t.Fatal("Expected 2 create request, got:", requests.createRequests)
	}
	if len(requests.deleteRequests) != 2 {
		t.Fatal("Expected 2 delete request, got:", requests.deleteRequests)
	}

	for idx, record := range records {
		if newRecords[idx].DNSName != record.DNSName {
			t.Error("DNS Name malformed on retrieval, got:", newRecords[idx].DNSName, "expected:", record.DNSName)
		}
		if newRecords[idx].Targets[0] != record.Targets[0] {
			t.Error("Targets malformed on retrieval, got:", newRecords[idx].Targets, "expected:", record.Targets)
		}
	}

	expectedCreateA := endpoint.Endpoint{
		DNSName:    "test2.example.com",
		Targets:    []string{"10.0.0.1", "10.0.0.2"},
		RecordType: endpoint.RecordTypeA,
	}
	expectedDeleteA := endpoint.Endpoint{
		DNSName:    "test2.example.com",
		Targets:    []string{"192.168.1.2"},
		RecordType: endpoint.RecordTypeA,
	}
	expectedCreateAAAA := endpoint.Endpoint{
		DNSName:    "test2.example.com",
		Targets:    []string{"fc00::1:10:0:0:1"},
		RecordType: endpoint.RecordTypeAAAA,
	}
	expectedDeleteAAAA := endpoint.Endpoint{
		DNSName:    "test2.example.com",
		Targets:    []string{"fc00::1:192:168:1:2"},
		RecordType: endpoint.RecordTypeAAAA,
	}

	for _, request := range requests.createRequests {
		switch request.RecordType {
		case endpoint.RecordTypeA:
			if !reflect.DeepEqual(request, &expectedCreateA) {
				t.Error("Unexpected create request, got:", request, "expected:", &expectedCreateA)
			}
		case endpoint.RecordTypeAAAA:
			if !reflect.DeepEqual(request, &expectedCreateAAAA) {
				t.Error("Unexpected create request, got:", request, "expected:", &expectedCreateAAAA)
			}
		default:
		}
	}

	for _, request := range requests.deleteRequests {
		switch request.RecordType {
		case endpoint.RecordTypeA:
			if !reflect.DeepEqual(request, &expectedDeleteA) {
				t.Error("Unexpected delete request, got:", request, "expected:", &expectedDeleteA)
			}
		case endpoint.RecordTypeAAAA:
			if !reflect.DeepEqual(request, &expectedDeleteAAAA) {
				t.Error("Unexpected delete request, got:", request, "expected:", &expectedDeleteAAAA)
			}
		default:
		}
	}

	requests.clear()
}
