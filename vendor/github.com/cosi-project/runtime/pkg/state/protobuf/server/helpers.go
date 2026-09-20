// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package server

import (
	"regexp"
	"regexp/syntax"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/resource"
)

// ConvertLabelQuery converts protobuf representation of LabelQuery to state representation.
func ConvertLabelQuery(terms []*v1alpha1.LabelTerm) ([]resource.LabelQueryOption, error) {
	labelOpts := make([]resource.LabelQueryOption, 0, len(terms))

	for _, term := range terms {
		var opts []resource.TermOption

		if term.Invert {
			opts = append(opts, resource.NotMatches)
		}

		// Value is a repeated field in the protobuf definition, but the operators below
		// require a single value, so reject the request instead of panicking on an empty one.
		switch term.Op { //nolint:exhaustive
		case v1alpha1.LabelTerm_EQUAL,
			v1alpha1.LabelTerm_LT,
			v1alpha1.LabelTerm_LTE,
			v1alpha1.LabelTerm_LT_NUMERIC,
			v1alpha1.LabelTerm_LTE_NUMERIC:
			if len(term.Value) == 0 {
				return nil, status.Errorf(codes.InvalidArgument, "missing value for label query operator: %v", term.Op)
			}
		}

		switch term.Op {
		case v1alpha1.LabelTerm_EQUAL:
			labelOpts = append(labelOpts, resource.LabelEqual(term.Key, term.Value[0], opts...))
		case v1alpha1.LabelTerm_EXISTS:
			labelOpts = append(labelOpts, resource.LabelExists(term.Key, opts...))
		case v1alpha1.LabelTerm_NOT_EXISTS: //nolint:staticcheck
			labelOpts = append(labelOpts, resource.LabelExists(term.Key, resource.NotMatches))
		case v1alpha1.LabelTerm_IN:
			labelOpts = append(labelOpts, resource.LabelIn(term.Key, term.Value, opts...))
		case v1alpha1.LabelTerm_LT:
			labelOpts = append(labelOpts, resource.LabelLT(term.Key, term.Value[0], opts...))
		case v1alpha1.LabelTerm_LTE:
			labelOpts = append(labelOpts, resource.LabelLTE(term.Key, term.Value[0], opts...))
		case v1alpha1.LabelTerm_LT_NUMERIC:
			labelOpts = append(labelOpts, resource.LabelLTNumeric(term.Key, term.Value[0], opts...))
		case v1alpha1.LabelTerm_LTE_NUMERIC:
			labelOpts = append(labelOpts, resource.LabelLTENumeric(term.Key, term.Value[0], opts...))
		default:
			return nil, status.Errorf(codes.Unimplemented, "unsupported label query operator: %v", term.Op)
		}
	}

	return labelOpts, nil
}

const (
	// maxRegexpLength is the maximum length of the ID query regexp.
	//
	// Parsing cost is roughly linear in the length of the pattern, so this bounds the
	// work done before the program size below can be estimated.
	maxRegexpLength = 4096

	// maxRegexpProgramSize is the maximum size (in instructions) of the compiled ID query regexp.
	//
	// Repetition expands a pattern far beyond its length: the parser bounds the expansion
	// only by its own ~3.3M instruction limit, so the length limit above is not by itself a
	// useful bound on the compiled program. Reaching that ceiling costs ~700MiB and a few
	// hundred milliseconds per query.
	maxRegexpProgramSize = 4096
)

// ConvertIDQuery converts protobuf representation of IDQuery to state representation.
func ConvertIDQuery(input *v1alpha1.IDQuery) ([]resource.IDQueryOption, error) {
	if input == nil || input.Regexp == "" {
		return nil, nil
	}

	if len(input.Regexp) > maxRegexpLength {
		return nil, status.Errorf(codes.InvalidArgument, "regexp is too long: %d > %d", len(input.Regexp), maxRegexpLength)
	}

	parsed, err := syntax.Parse(input.Regexp, syntax.Perl)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse regexp: %v", err)
	}

	if size := regexpProgramSize(parsed); size > maxRegexpProgramSize {
		return nil, status.Errorf(codes.InvalidArgument, "regexp is too complex: %d > %d", size, maxRegexpProgramSize)
	}

	re, err := regexp.Compile(input.Regexp)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to compile regexp: %v", err)
	}

	return []resource.IDQueryOption{resource.IDRegexpMatch(re)}, nil
}

// regexpProgramSize estimates the number of instructions the parsed regexp compiles to.
//
// This is a port of (*parser).calcSize from regexp/syntax, which the parser itself uses to
// enforce its (much higher) built-in limit. It is accurate to within the couple of extra
// instructions the compiler emits for the program as a whole, and unlike compiling the
// regexp it doesn't allocate the program. Recursion is bounded by the nesting depth limit
// enforced by the parser.
func regexpProgramSize(re *syntax.Regexp) int64 {
	var size int64

	switch re.Op { //nolint:exhaustive
	case syntax.OpLiteral:
		size = int64(len(re.Rune))
	case syntax.OpCapture, syntax.OpStar:
		// star can be 1+ or 2+; assume 2 pessimistically
		size = 2 + regexpProgramSize(re.Sub[0])
	case syntax.OpPlus, syntax.OpQuest:
		size = 1 + regexpProgramSize(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			size += regexpProgramSize(sub)
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			size += regexpProgramSize(sub)
		}

		if len(re.Sub) > 1 {
			size += int64(len(re.Sub)) - 1
		}
	case syntax.OpRepeat:
		sub := regexpProgramSize(re.Sub[0])

		if re.Max == -1 {
			if re.Min == 0 {
				size = 2 + sub // x*
			} else {
				size = 1 + int64(re.Min)*sub // xxx+
			}

			break
		}

		// x{2,5} = xx(x(x(x)?)?)?
		size = int64(re.Max)*sub + int64(re.Max-re.Min)
	}

	return max(1, size)
}
