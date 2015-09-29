// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// Contributor:
// - Aaron Meihm ameihm@mozilla.com

package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/lib/pq"
	elastigo "github.com/mattbaird/elastigo/lib"
	"os"
	"strings"
)

var dbconn *sql.DB

type rra struct {
	Details rraDetails `json:"details"`
}

func (r *rra) sanitize() error {
	r.Details.Metadata.Service = strings.Replace(r.Details.Metadata.Service, "\n", " ", -1)
	r.Details.Metadata.Service = strings.TrimSpace(r.Details.Metadata.Service)
	return nil
}

type rraDetails struct {
	Metadata rraMetadata `json:"metadata"`
	Risk     rraRisk     `json:"risk"`
	Data     rraData     `json:"data"`
}

type rraMetadata struct {
	Service string `json:"service"`
}

type rraData struct {
	Default string `json:"default"`
}

type rraRisk struct {
	Confidentiality rraRiskAttr `json:"confidentiality"`
	Integrity       rraRiskAttr `json:"integrity"`
	Availability    rraRiskAttr `json:"availability"`
}

type rraRiskAttr struct {
	Reputation   rraMeasure `json:"reputation"`
	Finances     rraMeasure `json:"finances"`
	Productivity rraMeasure `json:"productivity"`
}

type rraMeasure struct {
	Impact string `json:"impact"`
}

var rraIndex = "rra"
var rraList []rra

func requestRRAs(eshost string) error {
	fmt.Fprintf(os.Stdout, "Requesting RRA list...\n")

	conn := elastigo.NewConn()
	conn.Domain = eshost

	template := `{
		"from": %v,
		"size": 10,
		"sort": [
		{ "details.metadata.service": "asc" }
		],
		"query": {
			"bool": {
				"must": [
					{
					"term": {
						"category": "rra_data"
					}},
					{
					"range": {
						"utctimestamp": {
							"gt": "now-7d"
						}
					}}
				]
			}
		}
	}`
	for i := 0; ; i += 10 {
		tempbuf := fmt.Sprintf(template, i)
		res, err := conn.Search(rraIndex, "rra_state", nil, tempbuf)
		if err != nil {
			return err
		}
		if res.Hits.Len() == 0 {
			break
		}
		for _, x := range res.Hits.Hits {
			var nrra rra
			err = json.Unmarshal(*x.Source, &nrra)
			if err != nil {
				return err
			}
			err = nrra.sanitize()
			if err != nil {
				return err
			}
			rraList = append(rraList, nrra)
		}
	}
	fmt.Fprintf(os.Stdout, "Fetched %v RRAs\n", len(rraList))

	return nil
}

func dbInit() error {
	var err error
	dbconn, err = sql.Open("postgres", "dbname=servicemap host=/var/run/postgresql")
	if err != nil {
		return err
	}
	return nil
}

func sanitizeImpact(s string) string {
	return strings.ToLower(s)
}

func dbUpdateRRAs() error {
	for _, x := range rraList {
		// Extract impact information.
		var (
			riskARI string
			riskAPI string
			riskAFI string

			riskCRI string
			riskCPI string
			riskCFI string

			riskIRI string
			riskIPI string
			riskIFI string

			datadef string
		)
		riskARI = sanitizeImpact(x.Details.Risk.Availability.Reputation.Impact)
		riskAPI = sanitizeImpact(x.Details.Risk.Availability.Productivity.Impact)
		riskAFI = sanitizeImpact(x.Details.Risk.Availability.Finances.Impact)

		riskCRI = sanitizeImpact(x.Details.Risk.Confidentiality.Reputation.Impact)
		riskCPI = sanitizeImpact(x.Details.Risk.Confidentiality.Productivity.Impact)
		riskCFI = sanitizeImpact(x.Details.Risk.Confidentiality.Finances.Impact)

		riskIRI = sanitizeImpact(x.Details.Risk.Integrity.Reputation.Impact)
		riskIPI = sanitizeImpact(x.Details.Risk.Integrity.Productivity.Impact)
		riskIFI = sanitizeImpact(x.Details.Risk.Integrity.Finances.Impact)

		datadef = sanitizeImpact(x.Details.Data.Default)

		fmt.Fprintf(os.Stdout, "RRA: %v\n", x.Details.Metadata.Service)
		_, err := dbconn.Exec(`INSERT INTO rra
			(service, ari, api, afi, cri, cpi, cfi, iri, ipi, ifi, datadefault)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
			WHERE NOT EXISTS (
				SELECT 1 FROM rra WHERE service = $12
			)`,
			x.Details.Metadata.Service, riskARI, riskAPI, riskAFI,
			riskCRI, riskCPI, riskCFI, riskIRI, riskIPI, riskIFI,
			datadef, x.Details.Metadata.Service)
		if err != nil {
			return err
		}
		_, err = dbconn.Exec(`UPDATE rra
			SET lastupdated = now() AT TIME ZONE 'utc'
			WHERE service = $1`, x.Details.Metadata.Service)
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	var eshost string
	flag.StringVar(&eshost, "e", "", "es hostname")
	flag.Parse()

	if eshost == "" {
		fmt.Fprintf(os.Stderr, "error: must specify es hostname with -e\n")
		os.Exit(1)
	}

	err := dbInit()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	err = requestRRAs(eshost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	err = dbUpdateRRAs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}