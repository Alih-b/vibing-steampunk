package adt

import (
	"context"
	"strings"
	"testing"
)

func TestDomainBody(t *testing.T) {
	b := domainBody(DomainOptions{Name: "ZDEMO", Description: "Demo & co", Package: "$TMP", DataType: "char",
		Length: 2, OutputLength: 2, Lowercase: true, FixedValues: []FixedValue{{Low: "A", Text: "Alpha"}, {Low: "B", High: "C", Text: "B<C"}}}, "DE")
	for _, want := range []string{
		`adtcore:name="ZDEMO"`, `adtcore:description="Demo &amp; co"`, `adtcore:masterLanguage="DE"`,
		`<doma:datatype>CHAR</doma:datatype><doma:length>000002</doma:length>`,
		`<doma:lowercase>true</doma:lowercase>`,
		`<doma:position>0002</doma:position><doma:low>B</doma:low><doma:high>C</doma:high><doma:text>B&lt;C</doma:text>`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("domain body lacks %s", want)
		}
	}
}

func TestDataElementBody(t *testing.T) {
	b := dataElementBody(DataElementOptions{Name: "ZDEMO", Description: "Demo", Package: "$TMP", Domain: "zdom",
		ShortLabel: "Demo", LongLabel: "Demo field"}, "EN")
	for _, want := range []string{
		`<dtel:typeKind>domain</dtel:typeKind><dtel:typeName>ZDOM</dtel:typeName>`,
		`<dtel:shortFieldLabel>Demo</dtel:shortFieldLabel><dtel:shortFieldLength>10</dtel:shortFieldLength><dtel:shortFieldMaxLength>10</dtel:shortFieldMaxLength>`,
		`<dtel:headingFieldMaxLength>55</dtel:headingFieldMaxLength>`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("data element body lacks %s", want)
		}
	}
	b = dataElementBody(DataElementOptions{Name: "ZDEMO", DataType: "char", Length: 15}, "EN")
	if !strings.Contains(b, `<dtel:typeKind>predefinedAbapType</dtel:typeKind><dtel:typeName></dtel:typeName><dtel:dataType>CHAR</dtel:dataType><dtel:dataTypeLength>000015</dtel:dataTypeLength>`) {
		t.Errorf("predefined type: %s", b)
	}
	b = dataElementBody(DataElementOptions{Name: "ZDEMO", Domain: "ZD"}, `E"&`)
	if !strings.Contains(b, `adtcore:language="E&quot;&amp;" adtcore:masterLanguage="E&quot;&amp;"`) {
		t.Errorf("language not escaped: %s", b)
	}
}

func TestDDICCreateRefusesBeforeSending(t *testing.T) {
	c, mock := newTransportTestClient(t, nil)
	for name, err := range map[string]error{
		"no type": func() error {
			_, e := c.CreateDomain(context.Background(), DomainOptions{Name: "ZD", Description: "d"})
			return e
		}(),
		"domain and type": func() error {
			_, e := c.CreateDataElement(context.Background(), DataElementOptions{Name: "ZE", Domain: "ZD", DataType: "CHAR"})
			return e
		}(),
		"neither": func() error {
			_, e := c.CreateDataElement(context.Background(), DataElementOptions{Name: "ZE"})
			return e
		}(),
		"short label long": func() error {
			_, e := c.CreateDataElement(context.Background(), DataElementOptions{Name: "ZE", Domain: "ZD", ShortLabel: "more than ten"})
			return e
		}(),
		"fixed value text": func() error {
			_, e := c.CreateDomain(context.Background(), DomainOptions{Name: "ZD", DataType: "CHAR", Length: 1, FixedValues: []FixedValue{{Low: "A", Text: strings.Repeat("x", 61)}}})
			return e
		}(),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(mock.requests) != 0 {
		t.Errorf("%d requests sent for invalid input", len(mock.requests))
	}
}
