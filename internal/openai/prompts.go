package openai

import (
	"fmt"
	"strings"
)

const BASE_SYSTEM = `You are a document information extraction engine.
You must output ONLY valid JSON and nothing else.
No markdown. No comments. No extra keys.
If a value is unknown, use null for strings and omit optional numeric fields when truly unavailable.
Dates must be ISO format YYYY-MM-DD when possible.`

const BASE_USER_TEMPLATE = `You will extract structured data from a document.
Return JSON that matches EXACTLY the schema below.

Rules:
- Output JSON only.
- Use the schema keys exactly.
- Do not add keys not in the schema.
- Numbers must be plain numbers, no currency symbols.
- confidence must be a number between 0 and 1.
- If you cannot find a required field, set it to null (for strings/dates) and set confidence below 0.6.

Document type: {{DOC_TYPE}}

Schema (JSON Schema):
{{JSON_SCHEMA}}

Document text:
{{DOC_TEXT}}

Return JSON only.`

const REPAIR_SYSTEM = `You are a strict JSON repair engine.
You receive an output that failed parsing or schema validation.
You must return ONLY corrected JSON that matches the provided schema exactly.
No markdown. No commentary. No extra keys. No surrounding text.`

const REPAIR_USER_TEMPLATE = `The previous model output was invalid or did not match the schema.

Schema (JSON Schema):
{{JSON_SCHEMA}}

Invalid output:
{{MODEL_OUTPUT}}

Fix the output so it matches the schema exactly.
Return JSON only.`

const CORRECT_SYSTEM = `You are a document extraction correction engine.
You must output ONLY valid JSON matching the provided schema exactly.
No markdown. No commentary. No extra keys.`

const CORRECT_USER_TEMPLATE = `The extracted JSON failed validation rules.
Correct ONLY the fields needed to satisfy the rules, using the document text as the source of truth.
If the document text does not support a correction with high confidence, keep the original value and lower confidence.

Document type: {{DOC_TYPE}}

Schema (JSON Schema):
{{JSON_SCHEMA}}

Document text:
{{DOC_TEXT}}

Current extracted JSON:
{{CURRENT_JSON}}

Failed rules:
{{FAILED_RULES}}

Return corrected JSON only.`

func RenderTemplate(tpl string, vars map[string]string) string {
	rendered := tpl
	for k, v := range vars {
		rendered = strings.ReplaceAll(rendered, "{{"+k+"}}", v)
	}
	return rendered
}

func BuildBaseUserPrompt(docType string, jsonSchema string, docText string) string {
	return RenderTemplate(BASE_USER_TEMPLATE, map[string]string{
		"DOC_TYPE":    docType,
		"JSON_SCHEMA": jsonSchema,
		"DOC_TEXT":    docText,
	})
}

func BuildRepairUserPrompt(jsonSchema string, modelOutput string) string {
	return RenderTemplate(REPAIR_USER_TEMPLATE, map[string]string{
		"JSON_SCHEMA":  jsonSchema,
		"MODEL_OUTPUT": modelOutput,
	})
}

func BuildCorrectUserPrompt(docType string, jsonSchema string, docText string, currentJSON string, failedRules []string) string {
	return RenderTemplate(CORRECT_USER_TEMPLATE, map[string]string{
		"DOC_TYPE":     docType,
		"JSON_SCHEMA":  jsonSchema,
		"DOC_TEXT":     docText,
		"CURRENT_JSON": currentJSON,
		"FAILED_RULES": fmt.Sprintf("%v", failedRules),
	})
}
