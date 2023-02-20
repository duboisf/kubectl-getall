package terminal

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TermInfo interface {
	Query(capname ...string) (string, error)
	QueryInt(capname string) (int, error)
}

type GetResourcesUpdate struct {
	Kind      string
	Resources int
}

type PBar interface {
	Increment(int)
	SetTotalIncrements(int)
	SetWidth(int)
	String() string
}

type UI struct {
	getResourcesUpdates chan *GetResourcesUpdate
	nbExecs, nbTputs    int
	progressBar         PBar
	termInfo            TermInfo
	termInfoCache       map[string]string
	totalKinds          chan int
	writer              *bufio.Writer
}

func NewUI(progressBar PBar, termInfo TermInfo, writer io.Writer) *UI {
	return &UI{
		progressBar:   progressBar,
		termInfo:      termInfo,
		termInfoCache: make(map[string]string),
		totalKinds:    make(chan int, 1),
		writer:        bufio.NewWriter(writer),
	}
}

func (u *UI) getTermSize() (x, y int, err error) {
	x, err = u.termInfo.QueryInt("lines")
	if err != nil {
		return 0, 0, err
	}
	y, err = u.termInfo.QueryInt("cols")
	if err != nil {
		return 0, 0, err
	}
	return
}

func (u *UI) queryTerminfo(capname string) string {
	output, found := u.termInfoCache[capname]
	if !found {
		u.nbTputs++
		var err error
		output, err = u.termInfo.Query(capname)
		if err != nil {
			return ""
		}
		u.termInfoCache[capname] = output
	}
	return output
}

func (u *UI) tput(capname string) {
	u.Print(u.queryTerminfo(capname))
}

// hideCursor uses tput to hide the cursor. It also starts a go routine to
// restore the cursor when the given context is done.
func (u *UI) hideCursor() {
	u.tput("civis")
}

func (u *UI) enterAlternateScreen() {
	u.tput("smcup")
}

func (u *UI) exitAlternateScreen() {
	u.tput("rmcup")
}

func (u *UI) showCursor() {
	u.tput("cvvis")
}

func (u *UI) Print(a ...any) {
	fmt.Fprint(u.writer, a...)
}

// Printf formats according to a format specifier and writes to standard output\.
// It returns the number of bytes written and any write error encountered\.
func (u *UI) Printf(template string, args ...interface{}) {
	fmt.Fprintf(u.writer, template, args...)
}

func (u *UI) Println(a ...interface{}) {
	fmt.Fprintln(u.writer, a...)
}

func (u *UI) moveCursorUp(lines int) {
	for i := 0; i < lines; i++ {
		u.tput("cuu1")
	}
}

func (u *UI) eraseCurrentLine() {
	u.Print("\r" + u.queryTerminfo("el"))
}

func (u *UI) eraseLastLines(count int) {
	u.Print("\r")
	for i := 0; i < count; i++ {
		u.eraseCurrentLine()
		u.moveCursorUp(1)
	}
	u.eraseCurrentLine()
}

func (u *UI) flush() error {
	return u.writer.Flush()
}

func (u *UI) SetTotalKinds(count int) chan<- *GetResourcesUpdate {
	u.totalKinds <- count
	u.getResourcesUpdates = make(chan *GetResourcesUpdate, count)
	return u.getResourcesUpdates
}

func (u *UI) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done() // important: do this last
	defer u.flush()
	defer u.showCursor()
	defer u.exitAlternateScreen()
	u.hideCursor()
	u.enterAlternateScreen()
	// lines, cols, _ := u.getTermSize()
	// cols, err := u.GetTermCols()
	// if err != nil {
	// 	cols = 30
	// }
	// windowSizeChange := make(chan os.Signal, 1)
	// signal.Notify(windowSizeChange, syscall.SIGWINCH)
	u.Printf("Discovering kinds...")
	u.flush()
	var totalKinds int
	select {
	case <-ctx.Done():
		return
	case totalKinds = <-u.totalKinds:
	}
	u.Printf(" found %d.\n", totalKinds)
	u.progressBar.SetWidth(10)
	u.progressBar.SetTotalIncrements(totalKinds)
	spinner := NewSpinner(100 * time.Millisecond)
	var processedKinds int
	var totalResourcesFound int
	var lastProcessedKind string
	formatWidth := len(strconv.Itoa(totalKinds))
	eraseLine := u.queryTerminfo("el")
	var progressLines []string
	for {
		u.tput("cup 0 0")
		progressLines = []string{
			fmt.Sprintf("Discovering kinds... found %d.\n", totalKinds),
			fmt.Sprintf("\r%s Fetched kinds: %s %*d/%d\n",
				spinner, u.progressBar.String(), formatWidth, processedKinds, totalKinds),
			fmt.Sprintf("Getting %s\n", lastProcessedKind),
			fmt.Sprintf("Total resources found: %4d", totalResourcesFound),
		}
		u.Print(strings.Join(progressLines, eraseLine))
		u.flush()
		select {
		case <-ctx.Done():
			return
		// case <-windowSizeChange:
		// 	newCols, err := u.termInfo.QueryInt("cols")
		// 	if err == nil {
		// 		cols = newCols
		// 		// progressBar.SetWidth(cols - 10)
		// 	}
		case getResourcesUpdate, more := <-u.getResourcesUpdates:
			if !more {
				return
			}
			u.progressBar.Increment(1)
			lastProcessedKind = getResourcesUpdate.Kind
			processedKinds++
			totalResourcesFound += getResourcesUpdate.Resources
		case <-spinner.Tick:
			spinner.Spin()
		}
	}
}
