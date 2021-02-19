package dynv6

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	urlutil "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

type zone struct {
	ID          int64
	Name        string
	IPv4address string
	IPv6prefix  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

var httpClient http.Client = http.Client{
	Timeout: time.Second * 60,
}

func checkStatusCode(resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("Unexpected status code: %s, could not read response: %w", resp.Status, err)
		}
		return fmt.Errorf("Unexpected status code: %s, response: %s", resp.Status, string(bodyBytes))
	}
	return nil
}

func (p *Provider) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	u, err := urlutil.Parse(url)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+p.Token)
	return req, nil
}

func (p *Provider) getZone(req *http.Request) (*zone, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err = checkStatusCode(resp); err != nil {
		return nil, err
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var z zone
	err = json.Unmarshal(bodyBytes, &z)
	if err != nil {
		return nil, err
	}
	return &z, nil
}

func (p *Provider) getZoneByName(ctx context.Context, zoneName string) (*zone, error) {
	// remove trailing dot
	zoneName = strings.TrimSuffix(zoneName, ".")
	req, err := p.newRequest(ctx, "GET", "https://dynv6.com/api/v2/zones/by-name/"+zoneName, nil)
	if err != nil {
		return nil, err
	}
	return p.getZone(req)
}

func (p *Provider) getZoneByID(ctx context.Context, zoneID int64) (*zone, error) {
	req, err := p.newRequest(ctx, "GET", "https://dynv6.com/api/v2/zones/"+fmt.Sprint(zoneID), nil)
	if err != nil {
		return nil, err
	}
	return p.getZone(req)
}

func (p *Provider) getZones(ctx context.Context) ([]zone, error) {
	req, err := p.newRequest(ctx, "GET", "https://dynv6.com/api/v2/zones", nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err = checkStatusCode(resp); err != nil {
		return nil, err
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var zones []zone
	err = json.Unmarshal(bodyBytes, &zones)
	if err != nil {
		return nil, err
	}
	return zones, nil
}

type record struct {
	ExpandedData string
	ID           int64
	ZoneID       int64
	Type         string
	Name         string
	Data         string
	Priority     int64
	Flags        int64
	Tag          string
	Weight       int64
	Port         int64
}

func (r *record) toLibdnsRecord() libdns.Record {
	return libdns.Record{
		ID:    fmt.Sprint(r.ID),
		Type:  r.Type,
		Name:  r.Name,
		Value: r.Data,
		TTL:   60 * time.Second, //dynv6 does not allow for custom TTL values
	}
}

func fromLibdnsRecord(zone string, rec *libdns.Record) (*record, error) {
	var (
		id  int64
		err error
	)
	if rec.ID != "" {
		id, err = strconv.ParseInt(rec.ID, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	return &record{
		ID:   id,
		Type: rec.Type,
		Name: strings.TrimSuffix(rec.Name, "."+strings.TrimSuffix(zone, ".")),
		Data: rec.Value,
	}, nil
}

func findRecord(recs []record, r *libdns.Record) *record {
	for _, v := range recs {
		if v.Type == r.Type && v.Name == r.Name {
			return &v
		}
	}
	return nil
}

func findRecordWithValue(recs []record, r *libdns.Record) *record {
	for _, v := range recs {
		if v.Type == r.Type && v.Name == r.Name && v.Data == r.Value {
			return &v
		}
	}
	return nil
}

func (p *Provider) getRecords(ctx context.Context, zoneID int64) ([]record, error) {
	url := fmt.Sprintf("https://dynv6.com/api/v2/zones/%d/records", zoneID)
	req, err := p.newRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err = checkStatusCode(resp); err != nil {
		return nil, err
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var records []record
	err = json.Unmarshal(bodyBytes, &records)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (p *Provider) deleteRecord(ctx context.Context, zoneID, recordID int64) error {
	url := fmt.Sprintf("https://dynv6.com/api/v2/zones/%d/records/%d", zoneID, recordID)
	req, err := p.newRequest(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = checkStatusCode(resp); err != nil {
		return err
	}
	return nil
}

func (p *Provider) addRecord(ctx context.Context, zoneID int64, rec *record) (*record, error) {
	url := fmt.Sprintf("https://dynv6.com/api/v2/zones/%d/records", zoneID)
	return p.addOrUpdateRecord(ctx, url, "POST", rec)
}

func (p *Provider) updateRecord(ctx context.Context, zoneID int64, rec *record) (*record, error) {
	url := fmt.Sprintf("https://dynv6.com/api/v2/zones/%d/records/%d", zoneID, rec.ID)
	return p.addOrUpdateRecord(ctx, url, "PATCH", rec)
}

func (p *Provider) addOrUpdateRecord(ctx context.Context, url, method string, rec *record) (*record, error) {
	jsonReq, err := json.Marshal(*rec)
	if err != nil {
		return nil, err
	}
	req, err := p.newRequest(ctx, method, url, bytes.NewBuffer(jsonReq))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err = checkStatusCode(resp); err != nil {
		return nil, err
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var returnedRecord record
	err = json.Unmarshal(bodyBytes, &returnedRecord)
	if err != nil {
		return nil, err
	}
	return &returnedRecord, nil
}
