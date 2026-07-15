package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const version = "0.3.1"

type conversionKind string

const (
	kindXmemlToFCP       conversionKind = "xmeml-to-fcpxml"
	kindFCPXMLToPremiere conversionKind = "fcpxml-to-xmeml"
)

func main() {
	args := filteredArgs(os.Args[1:])
	if len(args) == 0 {
		runGUI()
		return
	}

	fs := flag.NewFlagSet("cutbridge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	input := fs.String("input", "", "Premiere/Resolve XML 或 Final Cut Pro FCPXML")
	output := fs.String("output", "", "输出文件")
	event := fs.String("event", "CutBridge Import", "导入 FCP 后的事件名称")
	showVersion := fs.Bool("version", false, "显示版本")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("CutBridge", version)
		return
	}
	if *input == "" {
		fmt.Fprintln(os.Stderr, "缺少 --input")
		os.Exit(2)
	}

	result, _, outputPath, err := convertAuto(*input, *output, *event)
	if err != nil {
		fail(err)
	}
	fmt.Println(result)
	_ = outputPath
}

func filteredArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-psn_") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func detectConversionKind(input string) (conversionKind, string, error) {
	resolved, err := resolveFCPXMLInput(input)
	if err != nil {
		return "", "", fmt.Errorf("无法读取输入文件：%w", err)
	}
	root, err := parseDOM(resolved)
	if err != nil {
		return "", "", err
	}
	switch root.Name {
	case "xmeml":
		return kindXmemlToFCP, resolved, nil
	case "fcpxml":
		return kindFCPXMLToPremiere, resolved, nil
	default:
		return "", resolved, fmt.Errorf("不支持的 XML 根节点 <%s>；需要 <xmeml> 或 <fcpxml>", root.Name)
	}
}

func convertAuto(input, output, event string) (string, conversionKind, string, error) {
	kind, resolved, err := detectConversionKind(input)
	if err != nil {
		return "", "", "", err
	}
	switch kind {
	case kindXmemlToFCP:
		result, out, err := convertXmemlToFCP(resolved, output, event)
		return result, kind, out, err
	case kindFCPXMLToPremiere:
		result, out, err := convertFCPXMLToPremiere(input, output)
		return result, kind, out, err
	default:
		return "", kind, "", fmt.Errorf("无法确定转换方向")
	}
}

func convertXmemlToFCP(input, output, event string) (string, string, error) {
	inAbs, err := filepath.Abs(input)
	if err != nil {
		return "", "", err
	}
	out := output
	if out == "" {
		ext := filepath.Ext(inAbs)
		out = inAbs[:len(inAbs)-len(ext)] + "_for_FCP.fcpxml"
	}
	out = ensureExtension(out, ".fcpxml")
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(event) == "" {
		event = "CutBridge Import"
	}

	p, err := parseXmeml(inAbs)
	if err != nil {
		return "", "", err
	}
	root := buildFCPXML(p, event)
	if errs := validateGenerated(root); len(errs) > 0 {
		return "", "", fmt.Errorf("生成的 FCPXML 未通过内部检查：%v", errs)
	}
	if err := writeFCPXML(root, outAbs); err != nil {
		return "", "", err
	}
	report := reportPathFor(outAbs)
	if err := writeReport(p, report, outAbs); err != nil {
		return "", "", err
	}

	clips, warnings := 0, 0
	for _, t := range p.Timelines {
		clips += len(t.Clips)
	}
	for _, i := range p.Issues {
		if i.Severity == "warning" {
			warnings++
		}
	}
	return fmt.Sprintf("转换方向：Premiere/Resolve XML → Final Cut Pro\n已生成：%s\n兼容性报告：%s\n序列：%d，片段：%d，警告：%d", outAbs, report, len(p.Timelines), clips, warnings), outAbs, nil
}

func convertFCPXMLToPremiere(input, output string) (string, string, error) {
	inputAbs, err := filepath.Abs(input)
	if err != nil {
		return "", "", err
	}
	resolved, err := resolveFCPXMLInput(inputAbs)
	if err != nil {
		return "", "", err
	}
	out := output
	if out == "" {
		ext := filepath.Ext(inputAbs)
		out = inputAbs[:len(inputAbs)-len(ext)] + "_for_Premiere.xml"
	}
	out = ensureExtension(out, ".xml")
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return "", "", err
	}

	p, sourceVersion, err := parseFCPXML(resolved)
	if err != nil {
		return "", "", err
	}
	root := buildXmeml(p)
	if errs := validateGeneratedXmeml(root); len(errs) > 0 {
		return "", "", fmt.Errorf("生成的 Premiere XML 未通过内部检查：%v", errs)
	}
	if err := writeXmeml(root, outAbs); err != nil {
		return "", "", err
	}
	report := reportPathFor(outAbs)
	if err := writeReverseReport(p, sourceVersion, report, outAbs); err != nil {
		return "", "", err
	}

	videoClips, audioClips, warnings := 0, 0, 0
	for _, t := range p.Timelines {
		for _, c := range t.Clips {
			if c.Kind == "video" {
				videoClips++
			} else if c.Kind == "audio" {
				audioClips++
			}
		}
	}
	for _, issue := range p.Issues {
		if issue.Severity == "warning" {
			warnings++
		}
	}
	return fmt.Sprintf("转换方向：Final Cut Pro FCPXML → Premiere Pro\n已生成：%s\n兼容性报告：%s\n序列：%d，视频编辑：%d，音频编辑：%d，警告：%d", outAbs, report, len(p.Timelines), videoClips, audioClips, warnings), outAbs, nil
}

func ensureExtension(path, extension string) string {
	if strings.EqualFold(filepath.Ext(path), extension) {
		return path
	}
	return path + extension
}

func runGUI() {
	inputPath, err := runAppleScript([]string{
		`on run`,
		`set inputFile to choose file with prompt "选择 Premiere/Resolve XML、Final Cut Pro FCPXML 或 .fcpxmld 资源包"`,
		`return POSIX path of inputFile`,
		`end run`,
	})
	if isUserCancel(err) {
		return
	}
	if err != nil {
		showGUIError("无法选择输入文件：" + err.Error())
		return
	}

	selectedPath := inputPath
	kind, _, err := detectConversionKind(selectedPath)
	if err != nil {
		showGUIError(err.Error())
		return
	}
	eventName := "CutBridge Import"
	if kind == kindXmemlToFCP {
		eventName, err = runAppleScript([]string{
			`on run`,
			`set d to display dialog "检测到 Premiere/Resolve xmeml。请输入导入 Final Cut Pro 后的事件名称：" default answer "CutBridge Import" with title "CutBridge 双向转换" buttons {"取消", "继续"} default button "继续" cancel button "取消"`,
			`return text returned of d`,
			`end run`,
		})
		if isUserCancel(err) {
			return
		}
		if err != nil {
			showGUIError("无法读取事件名称：" + err.Error())
			return
		}
		if strings.TrimSpace(eventName) == "" {
			eventName = "CutBridge Import"
		}
	}

	baseName := strings.TrimSuffix(filepath.Base(selectedPath), filepath.Ext(selectedPath))
	prompt := "保存转换后的文件"
	if kind == kindXmemlToFCP {
		baseName += "_for_FCP.fcpxml"
		prompt = "保存 Final Cut Pro FCPXML"
	} else {
		baseName += "_for_Premiere.xml"
		prompt = "保存 Premiere Pro XML"
	}
	folder := filepath.Dir(selectedPath)
	outputPath, err := runAppleScript([]string{
		`on run argv`,
		`set folderPath to item 1 of argv`,
		`set defaultName to item 2 of argv`,
		`set promptText to item 3 of argv`,
		`set outputFile to choose file name with prompt promptText default location (POSIX file folderPath as alias) default name defaultName`,
		`return POSIX path of outputFile`,
		`end run`,
	}, folder, baseName, prompt)
	if isUserCancel(err) {
		return
	}
	if err != nil {
		showGUIError("无法选择输出位置：" + err.Error())
		return
	}

	result, _, finalOutput, err := convertAuto(selectedPath, outputPath, eventName)
	if err != nil {
		showGUIError(err.Error())
		return
	}

	_, _ = runAppleScript([]string{
		`on run argv`,
		`set outputPath to item 1 of argv`,
		`set resultText to item 2 of argv`,
		`set d to display dialog "转换完成。" & return & return & resultText with title "CutBridge 0.3.0" buttons {"完成", "在 Finder 中显示"} default button "在 Finder 中显示"`,
		`if button returned of d is "在 Finder 中显示" then do shell script "/usr/bin/open -R " & quoted form of outputPath`,
		`end run`,
	}, finalOutput, result)
}

func runAppleScript(lines []string, args ...string) (string, error) {
	cmdArgs := make([]string, 0, len(lines)*2+len(args))
	for _, line := range lines {
		cmdArgs = append(cmdArgs, "-e", line)
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("/usr/bin/osascript", cmdArgs...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return text, nil
}

func isUserCancel(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "(-128)") || strings.Contains(strings.ToLower(msg), "user canceled") || strings.Contains(msg, "用户已取消")
}

func showGUIError(message string) {
	_, _ = runAppleScript([]string{
		`on run argv`,
		`display alert "转换失败" message (item 1 of argv) as critical buttons {"好"} default button "好"`,
		`end run`,
	}, message)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
