package helm

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var valuesDescriptionRegex = regexp.MustCompile("^\\s*#\\s*(.*)\\s+--\\s*(.*)$")
var sectionTitleRegex = regexp.MustCompile("^\\s*#\\s+@section\\s*(.*)$")
var sectionDescriptionRegex = regexp.MustCompile("^\\s*#\\s+@section")
var commentContinuationRegex = regexp.MustCompile("^\\s*#(\\s?)(.*)$")
var defaultValueRegex = regexp.MustCompile("^\\s*# @default -- (.*)$")
var valueTypeRegex = regexp.MustCompile("^\\((.*?)\\)\\s*(.*)$")
var valueNotationTypeRegex = regexp.MustCompile("^\\s*#\\s+@notationType\\s+--\\s+(.*)$")

type ChartMetaMaintainer struct {
	Email string
	Name  string
	Url   string
}

type ChartMeta struct {
	ApiVersion  string `yaml:"apiVersion"`
	AppVersion  string `yaml:"appVersion"`
	KubeVersion string `yaml:"kubeVersion"`
	Name        string
	Deprecated  bool
	Description string
	Version     string
	Home        string
	Type        string
	Sources     []string
	Engine      string
	Maintainers []ChartMetaMaintainer
}

type ChartRequirementsItem struct {
	Name       string
	Version    string
	Repository string
	Alias      string
}

type ChartRequirements struct {
	Dependencies []ChartRequirementsItem
}

type ChartValueDescription struct {
	Description  string
	Default      string
	ValueType    string
	NotationType string
}

type ChartDocumentationInfo struct {
	ChartMeta
	ChartRequirements

	ChartDirectory          string
	ChartValues             *yaml.Node
	ChartValuesSections     map[int]string
	ChartValuesDescriptions map[string]ChartValueDescription
}

func getYamlFileContents(filename string) ([]byte, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, err
	}

	yamlFileContents, err := ioutil.ReadFile(filename)
	if err != nil {
		panic(err)
	}

	return []byte(strings.Replace(string(yamlFileContents), "\r\n", "\n", -1)), nil
}

func isErrorInReadingNecessaryFile(filePath string, loadError error) bool {
	if loadError != nil {
		if os.IsNotExist(loadError) {
			log.Warnf("Required chart file %s missing. Skipping documentation for chart", filePath)
			return true
		} else {
			log.Warnf("Error occurred in reading chart file %s. Skipping documentation for chart", filePath)
			return true
		}
	}

	return false
}

func parseChartFile(chartDirectory string) (ChartMeta, error) {
	chartYamlPath := filepath.Join(chartDirectory, "Chart.yaml")
	chartMeta := ChartMeta{}
	yamlFileContents, err := getYamlFileContents(chartYamlPath)

	if isErrorInReadingNecessaryFile(chartYamlPath, err) {
		return chartMeta, err
	}

	err = yaml.Unmarshal(yamlFileContents, &chartMeta)
	return chartMeta, err
}

func requirementKey(requirement ChartRequirementsItem) string {
	return fmt.Sprintf("%s/%s", requirement.Repository, requirement.Name)
}

func parseChartRequirementsFile(chartDirectory string, apiVersion string) (ChartRequirements, error) {
	var requirementsPath string

	if apiVersion == "v1" {
		requirementsPath = filepath.Join(chartDirectory, "requirements.yaml")

		if _, err := os.Stat(requirementsPath); os.IsNotExist(err) {
			return ChartRequirements{Dependencies: []ChartRequirementsItem{}}, nil
		}
	} else {
		requirementsPath = filepath.Join(chartDirectory, "Chart.yaml")
	}

	chartRequirements := ChartRequirements{}
	yamlFileContents, err := getYamlFileContents(requirementsPath)

	if isErrorInReadingNecessaryFile(requirementsPath, err) {
		return chartRequirements, err
	}

	err = yaml.Unmarshal(yamlFileContents, &chartRequirements)
	if err != nil {
		return chartRequirements, err
	}

	sort.Slice(chartRequirements.Dependencies[:], func(i, j int) bool {
		return requirementKey(chartRequirements.Dependencies[i]) < requirementKey(chartRequirements.Dependencies[j])
	})

	return chartRequirements, nil
}

func printNode(node *yaml.Node, prefix string) {
	fmt.Println(prefix, "Kind: ", node.Kind)
	fmt.Println(prefix, "Style: ", node.Style)
	fmt.Println(prefix, "Tag: ", node.Tag)
	fmt.Println(prefix, "Value: ", node.Value)
	// fmt.Println(prefix, "Anchor: ", node.Anchor)
	// fmt.Println(prefix, "Alias: ", node.Alias)
	fmt.Println(prefix, "Content: ", node.Content)
	fmt.Println(prefix, "HeadComment: ", node.HeadComment)
	// fmt.Println(prefix, "LineComment: ", node.LineComment)
	// fmt.Println(prefix, "FootComment: ", node.FootComment)
	fmt.Println(prefix, "Line: ", node.Line)
	fmt.Println(prefix, "Column: ", node.Column)
}

func printNodes(node *yaml.Node, prefix string) {
	printNode(node, prefix)
	for i, n := range node.Content {
		printNodes(n, prefix+"   "+strconv.Itoa(i)+" ")
	}
}

func parseChartValuesFile(chartDirectory string) (yaml.Node, error) {
	valuesPath := filepath.Join(chartDirectory, viper.GetString("values-file"))
	yamlFileContents, err := getYamlFileContents(valuesPath)

	var values yaml.Node
	if isErrorInReadingNecessaryFile(valuesPath, err) {
		return values, err
	}

	err = yaml.Unmarshal(yamlFileContents, &values)
	// fmt.Println("DocumentNode: ", yaml.DocumentNode)
	// fmt.Println("SequenceNode: ", yaml.SequenceNode)
	// fmt.Println("MappingNode: ", yaml.MappingNode)
	// fmt.Println("ScalarNode: ", yaml.ScalarNode)
	// fmt.Println("AliasNode: ", yaml.AliasNode)
	// printNodes(&values, "")
	// fmt.Println(len(values.Content[0].Content))
	return values, err
}

func parseChartValuesFileComments(chartDirectory string) (map[string]ChartValueDescription, map[int]string, error) {
	valuesPath := filepath.Join(chartDirectory, viper.GetString("values-file"))
	valuesFile, err := os.Open(valuesPath)

	if isErrorInReadingNecessaryFile(valuesPath, err) {
		return map[string]ChartValueDescription{}, map[int]string{}, err
	}

	defer valuesFile.Close()

	keyToDescriptions := make(map[string]ChartValueDescription)
	scanner := bufio.NewScanner(valuesFile)
	foundValuesComment := false
	commentLines := make([]string, 0)
	lineNoToSection := make(map[int]string)
	var lineNo int = 0

	for scanner.Scan() {
		lineNo++
		currentLine := scanner.Text()

		// If we've not yet found a values comment with a key name, try and find one on each line
		if !foundValuesComment {
			// First check if this line is not a new section
			sectionMatch := sectionTitleRegex.FindStringSubmatch(currentLine)
			fmt.Println(sectionMatch)
			if len(sectionMatch) == 2 && sectionMatch[1] != "" {
				lineNoToSection[lineNo] = sectionMatch[1]
				continue
			}

			match := valuesDescriptionRegex.FindStringSubmatch(currentLine)
			if len(match) < 3 {
				continue
			}
			if match[1] == "" {
				continue
			}

			foundValuesComment = true
			commentLines = append(commentLines, currentLine)
			continue
		}

		// If we've already found a values comment, on the next line try and parse a custom default value. If we find one
		// that completes parsing for this key, add it to the list and reset to searching for a new key
		defaultCommentMatch := defaultValueRegex.FindStringSubmatch(currentLine)
		commentContinuationMatch := commentContinuationRegex.FindStringSubmatch(currentLine)

		if len(defaultCommentMatch) > 1 || len(commentContinuationMatch) > 1 {
			commentLines = append(commentLines, currentLine)
			continue
		}

		// If we haven't continued by this point, we didn't match any of the comment formats we want, so we need to add
		// the in progress value to the map, and reset to looking for a new key
		key, description := ParseComment(commentLines)
		keyToDescriptions[key] = description
		commentLines = make([]string, 0)
		foundValuesComment = false
	}
	fmt.Println(lineNoToSection)
	return keyToDescriptions, lineNoToSection, nil
}

func ParseChartInformation(chartDirectory string) (ChartDocumentationInfo, error) {
	var chartDocInfo ChartDocumentationInfo
	var err error

	chartDocInfo.ChartDirectory = chartDirectory
	chartDocInfo.ChartMeta, err = parseChartFile(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	chartDocInfo.ChartRequirements, err = parseChartRequirementsFile(chartDirectory, chartDocInfo.ApiVersion)
	if err != nil {
		return chartDocInfo, err
	}

	chartValues, err := parseChartValuesFile(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	chartDocInfo.ChartValues = &chartValues
	chartDocInfo.ChartValuesDescriptions, chartDocInfo.ChartValuesSections, err = parseChartValuesFileComments(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	return chartDocInfo, nil
}
