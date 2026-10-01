package adt

import (
	"fmt"
	"strings"
)

// messageClassCreateBody is the creation document of a message class (SE91).
// The messages themselves are written afterwards with WriteMessageClassTexts.
func messageClassCreateBody(opts CreateObjectOptions, typeInfo objectTypeInfo, responsible string) string {
	lang := strings.ToUpper(strings.TrimSpace(opts.MasterLanguage))
	if lang == "" {
		lang = "EN"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:language="%s"
  adtcore:masterLanguage="%s"
  adtcore:responsible="%s">
  <adtcore:packageRef adtcore:name="%s"/>
</%s>`,
		typeInfo.rootName, typeInfo.namespace,
		escapeXML(opts.Description),
		strings.ToUpper(opts.Name),
		opts.ObjectType,
		lang, lang,
		escapeXML(responsible),
		escapeXML(strings.ToUpper(opts.PackageName)),
		typeInfo.rootName)
}
