package gendoc

import (
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/golang/protobuf/proto"
	plugin_go "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/pseudomuto/protokit"
)

// PluginOptions encapsulates options for the plugin. The type of renderer, template file, and the name of the output
// file are included.
type PluginOptions struct {
	Type            RenderType
	TemplateFile    string
	OutputFile      string
	ExcludePatterns []*regexp.Regexp
	SourceRelative  bool
}

// SupportedFeatures describes a flag setting for supported features.
var SupportedFeatures = uint64(plugin_go.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

// Plugin describes a protoc code generate plugin. It's an implementation of Plugin from github.com/pseudomuto/protokit
type Plugin struct{}

// Generate compiles the documentation and generates the CodeGeneratorResponse to send back to protoc. It does this
// by rendering a template based on the options parsed from the CodeGeneratorRequest.
func (p *Plugin) Generate(r *plugin_go.CodeGeneratorRequest) (*plugin_go.CodeGeneratorResponse, error) {
	options, err := ParseOptions(r)
	if err != nil {
		return nil, err
	}

	result := excludeUnwantedProtos(protokit.ParseCodeGenRequest(r), options.ExcludePatterns)

	customTemplate := ""

	if options.TemplateFile != "" {
		data, err := ioutil.ReadFile(options.TemplateFile)
		if err != nil {
			return nil, err
		}

		customTemplate = string(data)
	}

	resp := new(plugin_go.CodeGeneratorResponse)
	fdsGroup := groupProtosByDirectory(result, options.SourceRelative)
	for dir, fds := range fdsGroup {
		template := NewTemplate(fds)

		output, err := RenderTemplate(options.Type, template, customTemplate)
		if err != nil {
			return nil, err
		}

		resp.File = append(resp.File, &plugin_go.CodeGeneratorResponse_File{
			Name:    proto.String(filepath.Join(dir, options.OutputFile)),
			Content: proto.String(string(output)),
		})
	}

	resp.SupportedFeatures = proto.Uint64(SupportedFeatures)

	return resp, nil
}

func groupProtosByDirectory(fds []*protokit.FileDescriptor, sourceRelative bool) map[string][]*protokit.FileDescriptor {
	fdsGroup := make(map[string][]*protokit.FileDescriptor)

	for _, fd := range fds {
		dir := ""
		if sourceRelative {
			dir, _ = filepath.Split(fd.GetName())
		}
		if dir == "" {
			dir = "./"
		}
		fdsGroup[dir] = append(fdsGroup[dir], fd)
	}
	return fdsGroup
}

func excludeUnwantedProtos(fds []*protokit.FileDescriptor, excludePatterns []*regexp.Regexp) []*protokit.FileDescriptor {
	descs := make([]*protokit.FileDescriptor, 0)

OUTER:
	for _, d := range fds {
		for _, p := range excludePatterns {
			if p.MatchString(d.GetName()) {
				continue OUTER
			}
		}

		descs = append(descs, d)
	}

	return descs
}

// ParseOptions parses plugin options from a CodeGeneratorRequest. It does this by splitting the `Parameter` field from
// the request object and parsing out the type of renderer to use and the name of the file to be generated.
//
// The parameter (`--doc_opt`) must be of the format <TYPE|TEMPLATE_FILE>,<OUTPUT_FILE>[,default|source_relative]:<EXCLUDE_PATTERN>,<EXCLUDE_PATTERN>*.
// The file will be written to the directory specified with the `--doc_out` argument to protoc.
func ParseOptions(req *plugin_go.CodeGeneratorRequest) (*PluginOptions, error) {
	options := &PluginOptions{
		Type:           RenderTypeHTML,
		TemplateFile:   "",
		OutputFile:     "index.html",
		SourceRelative: false,
	}

	params := req.GetParameter()
	if strings.Contains(params, ":") {
		// Parse out exclude patterns if any
		parts := strings.Split(params, ":")

		// On Windows, there can legitimately be up to two ":" in the first part: one for each filename in case absolute paths
		// are used, as a divider between the drive letter and the remainder of the path. That makes it really ugly: we now 
		// need to do some heuristics to "match" Windows path patterns to detect if a : is not to be treated as a divider
		// between the first parameter half and the second half with the exclude patterns.
		// This fixes GitHub issue #497.
		winPathHeuristic := func (first, second string) bool {
			// A Windows path in our doc_opt parameter is assumed to have a backslash in the second part...
			if strings.HasPrefix(second, "\\") {
				// ...and a drive letter either directly at the start of the first part or being preceded by a comma.
				// If both of these conditions match, it is assumed that the : separation actually splitted a Windows
				// path in two parts which belong together.
				firstMatches, _ := regexp.MatchString("(?:^|,)[a-zA-z]$", first)
				return firstMatches;
			}
			return false;
		}
		excludePart := ""
		if winPathHeuristic(parts[0], parts[1]) {
			params = parts[0] + ":" + parts[1]
			if len(parts) > 2 {
				if winPathHeuristic(params, parts[2]) {
					params = params + ":" + parts[2]
					if len(parts) > 3 {
						excludePart = parts[3]
					}
				} else {
					excludePart = parts[2]
				}
			}
		} else {
			params = parts[0]
			excludePart = parts[1]
		}

		if len(excludePart) > 0 {
			for _, pattern := range strings.Split(excludePart, ",") {
				r, err := regexp.Compile(pattern)
				if err != nil {
					return nil, err
				}
				options.ExcludePatterns = append(options.ExcludePatterns, r)
			}
		}
	}
	if params == "" {
		return options, nil
	}

	if !strings.Contains(params, ",") {
		return nil, fmt.Errorf("Invalid parameter: %s", params)
	}

	parts := strings.Split(params, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("Invalid parameter: %s", params)
	}

	options.TemplateFile = parts[0]
	options.OutputFile = path.Base(parts[1])
	if len(parts) > 2 {
		switch parts[2] {
		case "source_relative":
			options.SourceRelative = true
		case "default":
			options.SourceRelative = false
		default:
			return nil, fmt.Errorf("Invalid parameter: %s", params)
		}
	}
	options.SourceRelative = len(parts) > 2 && parts[2] == "source_relative"

	renderType, err := NewRenderType(options.TemplateFile)
	if err == nil {
		options.Type = renderType
		options.TemplateFile = ""
	}

	return options, nil
}
