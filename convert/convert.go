package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	ctyconvert "github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

const (
	LogColor     = "\033[1;32m%s\033[0m"
	LogColor2    = "\033[1;34m%s\033[0m"
	LogColor3    = "\033[1;36m%s\033[0m"
	WarningColor = "\033[1;33m[Warning]\033[0m"
	ErrorColor   = "\033[1;31m[Error]\033a[0m"
)

// Bytes takes the contents of an HCL file, as bytes, and converts
// them into a JSON representation of the HCL file.
func HclToJson(bytes []byte, filename string) ([]byte, error) {
	file, diags := hclsyntax.ParseConfig(bytes, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse config: %v", diags.Errs())
	}

	hclBytes, err := File(file)
	if err != nil {
		return nil, fmt.Errorf("convert to HCL: %w", err)
	}

	return hclBytes, nil
}

// File takes an HCL file and converts it to its JSON representation.
func File(file *hcl.File) ([]byte, error) {
	convertedFile, err := ConvertFile(file)
	if err != nil {
		return nil, fmt.Errorf("convert file: %w", err)
	}

	// MEMO : json marshall할 때 encoder의 옵션 escapehtml을 false로 설정.
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encodeErr := encoder.Encode(convertedFile)
	if encodeErr != nil {
		return nil, fmt.Errorf("marshal json : %w", err)
	}
	jsonBytes := buffer.Bytes()

	return jsonBytes, nil
}

type jsonObj = map[string]interface{}

type converter struct {
	bytes []byte
}

func ConvertFile(file *hcl.File) (jsonObj, error) {
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("convert file body to body type")
	}

	c := converter{
		bytes: file.Bytes,
	}

	out, err := c.convertBody(body)
	if err != nil {
		return nil, fmt.Errorf("convert body: %w", err)
	}

	return out, nil
}

func (c *converter) convertBody(body *hclsyntax.Body) (jsonObj, error) {
	out := make(jsonObj)

	for _, block := range body.Blocks {
		fmt.Printf(LogColor2, "Convert Block : ")
		fmt.Println("Type => '"+block.Type+"', Labels =>", block.Labels)
		if err := c.convertBlock(block, out); err != nil {
			return nil, fmt.Errorf("Unable to convert block: %w", err)
		}
	}

	var err error
	for key, value := range body.Attributes {
		fmt.Printf(LogColor2, "Convert Expression : ")
		fmt.Println(key)
		out[key], err = c.convertExpression(value.Expr)
		if err != nil {
			return nil, fmt.Errorf("Unable to convert expression: %w", err)
		}
	}

	return out, nil
}

func (c *converter) rangeSource(r hcl.Range) string {
	// for some reason the range doesn't include the ending paren, so
	// check if the next character is an ending paren, and include it if it is.
	end := r.End.Byte
	if end < len(c.bytes) && c.bytes[end] == ')' {
		end++
	}
	return string(c.bytes[r.Start.Byte:end])
}

func (c *converter) convertBlock(block *hclsyntax.Block, out jsonObj) error {
	key := block.Type
	for _, label := range block.Labels {

		// Labels represented in HCL are defined as quoted strings after the name of the block:
		// block "label_one" "label_two"
		//
		// Labels represtend in JSON are nested one after the other:
		// "label_one": {
		//   "label_two": {}
		// }
		//
		// To create the JSON representation, check to see if the label exists in the current output:
		//
		// When the label exists, move onto the next label reference.
		// When a label does not exist, create the label in the output and set that as the next label reference
		// in order to append (potential) labels to it.
		if _, exists := out[key]; exists {
			var ok bool
			out, ok = out[key].(jsonObj)
			if !ok {
				return fmt.Errorf("Unable to convert Block to JSON: %v.%v", block.Type, strings.Join(block.Labels, "."))
			}
		} else {
			out[key] = make(jsonObj)
			out = out[key].(jsonObj)
		}

		key = label
	}

	value, err := c.convertBody(block.Body)
	if err != nil {
		return fmt.Errorf("convert body: %w", err)
	}

	// Multiple blocks can exist with the same name, at the same
	// level in the JSON document (e.g. locals).
	//
	// For consistency, always wrap the value in a collection.
	// When multiple values are at the same key
	if current, exists := out[key]; exists {
		// MEMO: Provider의 경우 중복된 키값으로 선언됨. 그럴 땐 terraform json syntax에 맞게 작성 되도록 처리해줌
		if reflect.TypeOf(out[key]) == reflect.TypeOf(map[string]interface{}{}) {
			var firstValue = out[key]
			out[key] = []interface{}{firstValue}
			current = out[key]
		}
		out[key] = append(current.([]interface{}), value)
	} else {
		// out[key] = []interface{}{value}
		out[key] = value
	}

	return nil
}

func (c *converter) convertExpression(expr hclsyntax.Expression) (interface{}, error) {

	// assume it is hcl syntax (because, um, it is)
	switch value := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		fmt.Printf(LogColor, "LiteralValueExpr: ")
		fmt.Println(expr.Range())
		return ctyjson.SimpleJSONValue{Value: value.Val}, nil
	case *hclsyntax.UnaryOpExpr:
		fmt.Printf(LogColor, "UnaryOpExpr: ")
		fmt.Println(expr.Range())
		return c.convertUnary(value)
	case *hclsyntax.TemplateExpr:
		fmt.Printf(LogColor, "TemplateExpr: ")
		fmt.Println(expr.Range())
		return c.convertTemplate(value)
	case *hclsyntax.TemplateWrapExpr:
		fmt.Printf(LogColor, "TemplateWrapExpr: ")
		fmt.Println(expr.Range())
		return c.convertExpression(value.Wrapped)
	case *hclsyntax.TupleConsExpr:
		fmt.Printf(LogColor, "TupleConsExpr: ")
		fmt.Println(expr.Range())
		list := make([]interface{}, 0)
		for _, ex := range value.Exprs {
			elem, err := c.convertExpression(ex)
			if err != nil {
				return nil, err
			}
			list = append(list, elem)
		}
		return list, nil
	case *hclsyntax.ObjectConsExpr:
		fmt.Printf(LogColor, "ObjectConsExpr: ")
		fmt.Println(expr.Range())
		m := make(jsonObj)
		for _, item := range value.Items {
			key, err := c.convertKey(item.KeyExpr)
			if err != nil {
				return nil, err
			}
			m[key], err = c.convertExpression(item.ValueExpr)
			if err != nil {
				return nil, err
			}
		}
		return m, nil
	default:
		fmt.Printf(LogColor, "Default: ")
		fmt.Println(expr.Range())
		return c.wrapExpr(expr), nil
	}
}

func (c *converter) convertUnary(v *hclsyntax.UnaryOpExpr) (interface{}, error) {
	_, isLiteral := v.Val.(*hclsyntax.LiteralValueExpr)
	if !isLiteral {
		// If the expression after the operator isn't a literal, fall back to
		// wrapping the expression with ${...}
		return c.wrapExpr(v), nil
	}
	val, err := v.Value(nil)
	if err != nil {
		return nil, err
	}
	return ctyjson.SimpleJSONValue{Value: val}, nil
}

func (c *converter) convertTemplate(t *hclsyntax.TemplateExpr) (string, error) {
	if t.IsStringLiteral() {
		// safe because the value is just the string
		v, err := t.Value(nil)
		if err != nil {
			return "", err
		}
		return v.AsString(), nil
	}
	var builder strings.Builder
	for _, part := range t.Parts {
		s, err := c.convertStringPart(part)
		if err != nil {
			return "", err
		}
		builder.WriteString(s)
	}
	return builder.String(), nil
}

func (c *converter) convertStringPart(expr hclsyntax.Expression) (string, error) {
	switch v := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		s, err := ctyconvert.Convert(v.Val, cty.String)
		if err != nil {
			return "", err
		}
		return s.AsString(), nil
	case *hclsyntax.TemplateExpr:
		return c.convertTemplate(v)
	case *hclsyntax.TemplateWrapExpr:
		return c.convertStringPart(v.Wrapped)
	case *hclsyntax.ConditionalExpr:
		return c.convertTemplateConditional(v)
	case *hclsyntax.TemplateJoinExpr:
		return c.convertTemplateFor(v.Tuple.(*hclsyntax.ForExpr))
	default:
		// treating as an embedded expression
		// MEMO : 만약 string안 변수만 다르게 감싸줘야 한다면 이 부분 wrapExprVarInString로 수정하기
		return c.wrapExprVarInString(expr), nil
	}
}

func (c *converter) convertKey(keyExpr hclsyntax.Expression) (string, error) {
	// a key should never have dynamic input
	if k, isKeyExpr := keyExpr.(*hclsyntax.ObjectConsKeyExpr); isKeyExpr {
		keyExpr = k.Wrapped
		if _, isTraversal := keyExpr.(*hclsyntax.ScopeTraversalExpr); isTraversal {
			return c.rangeSource(keyExpr.Range()), nil
		}
	}
	return c.convertStringPart(keyExpr)
}

func (c *converter) convertTemplateConditional(expr *hclsyntax.ConditionalExpr) (string, error) {
	var builder strings.Builder
	builder.WriteString("%{if ")
	builder.WriteString(c.rangeSource(expr.Condition.Range()))
	builder.WriteString("}")
	trueResult, err := c.convertStringPart(expr.TrueResult)
	if err != nil {
		return "", nil
	}
	builder.WriteString(trueResult)
	falseResult, err := c.convertStringPart(expr.FalseResult)
	if len(falseResult) > 0 {
		builder.WriteString("%{else}")
		builder.WriteString(falseResult)
	}
	builder.WriteString("%{endif}")

	return builder.String(), nil
}

func (c *converter) convertTemplateFor(expr *hclsyntax.ForExpr) (string, error) {
	var builder strings.Builder
	builder.WriteString("%{for ")
	if len(expr.KeyVar) > 0 {
		builder.WriteString(expr.KeyVar)
		builder.WriteString(", ")
	}
	builder.WriteString(expr.ValVar)
	builder.WriteString(" in ")
	builder.WriteString(c.rangeSource(expr.CollExpr.Range()))
	builder.WriteString("}")
	templ, err := c.convertStringPart(expr.ValExpr)
	if err != nil {
		return "", err
	}
	builder.WriteString(templ)
	builder.WriteString("%{endfor}")

	return builder.String(), nil
}

func (c *converter) wrapExpr(expr hclsyntax.Expression) string {
	return "${" + c.rangeSource(expr.Range()) + "}"
}

// MEMO : string안에 있는 ${}변수에 대해선 hcl->json replaceAll에서 변환 안되게 하기 위해 다른 기호로 wrapping함.
// MEMO : 근데 변수랑 함수들 ${}로 감싸져도 테라폼에서 동작하면 굳이 다르게 안넣어줘도 될듯? 일단 뺌 => 이렇게 생각했으나, 함수 감싼 ${}는 없애줘야 할듯해서 다시 이거 사용함.
func (c *converter) wrapExprVarInString(expr hclsyntax.Expression) string {
	return "@@@{" + c.rangeSource(expr.Range()) + "}@@@"
}
