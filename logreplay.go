package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
	"time"
)

const (
	MAX_DISK_READ_BYTES  = 1024 * 1024
	MIN_LINES_PER_SECOND = 0.000001

	WIDGET_SIZE = 12

	//at full throttle my laptop prints ~388 kLines/sec. that is < (1 << 19)
	// Mind that this may have to be increased on other systems though!
	PAST_OUTPUT_CAPACITY = 1 << 20
	INTERVAL_FRACTION    = 0.1
)

var WidgetFrames = [...]string{
	"<^--------->",
	"<-^-------->",
	"<--^------->",
	"<---^------>",
	"<----^----->",
	"<-----^---->",
	"<------^--->",
	"<-------^-->",
	"<--------^->",
	"<---------^>",
}

type outputMetadata struct {
	timestamp time.Time
	bytes     int
}

// Cyclic buffer of previously written lines. Next line will be stored at writeIdx index.
type pastOutputBuff struct {
	writeIdx int
	size     int
	entries  [PAST_OUTPUT_CAPACITY]outputMetadata
}

func (buff *pastOutputBuff) add(entry outputMetadata) {
	buff.entries[buff.writeIdx] = entry

	buff.writeIdx++
	// same as                    % PAST_OUTPUT_CAPACITY
	buff.writeIdx = buff.writeIdx & (PAST_OUTPUT_CAPACITY - 1)

	buff.size++
	if buff.size > PAST_OUTPUT_CAPACITY {
		buff.size = PAST_OUTPUT_CAPACITY
	}
}

func (buff *pastOutputBuff) statsInLast(duration time.Duration) (totalLines int, bytes int) {
	before := time.Now().Add(-duration)

	// go backwards in time until last more than one second reached
	for ; totalLines < buff.size; totalLines++ {
		idxToEntries := (buff.writeIdx - 1 - totalLines) & (PAST_OUTPUT_CAPACITY - 1)

		meta := buff.entries[idxToEntries]
		if meta.timestamp.Before(before) {
			break
		}

		bytes += meta.bytes
	}
	return
}

func main() {
	if len(os.Args) != 3 {
		fmt.Printf(`Usage is:
		logreplay original.log liveReplayed.log`)
		os.Exit(0)
	}

	originalFile, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer originalFile.Close()

	replayedFile, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	defer replayedFile.Close()

	linesPerSecondChan := make(chan float64)
	widgetOrRatesChan := make(chan bool)
	endOfFileReached := make(chan bool)
	go replayFile(originalFile, replayedFile, linesPerSecondChan, widgetOrRatesChan, endOfFileReached)
	go ReadStdIn(linesPerSecondChan, widgetOrRatesChan)

	<-endOfFileReached
}

func ReadStdIn(linesPerSecondChan chan float64, widgetOrRatesChan chan bool) {
	scanner := bufio.NewScanner(os.Stdin)
	var Quit bool = false
	for !Quit {
		scanner.Scan()
		text := scanner.Text()
		if text == "t" {
			widgetOrRatesChan <- true
			continue
		}

		rate, err := strconv.ParseFloat(text, 32)
		if err == nil {
			linesPerSecondChan <- rate
		}
	}
}

func nextLine(src []byte) (line, rest []byte) {
	for i, char := range src {
		if char == '\n' {
			return src[0 : i+1], src[i+1:]
		}
	}
	// do return partial lines please!
	return src, src[len(src):]
}

func replayFile(originalFile, replayedFile *os.File, linesPerSecChan <-chan float64, widgetOrRatesChan <-chan bool, endOfFileChan chan<- bool) {
	inBuff := make([]byte, MAX_DISK_READ_BYTES)

	lineNo := 0

	linesPerSecondTarget := 10.0

	// false - prints rates per second
	// true - prints rate widget
	printWidgetOrRates := false

	targetPrintPeriod := time.Nanosecond * time.Duration(1_000_000_000*1/linesPerSecondTarget)
	fmt.Printf("linesPerSecond: %f; targetPrintPeriod: %v\n", linesPerSecondTarget, targetPrintPeriod)

	sleepQuantum := benchmarkSleepQuantum()
	fmt.Printf("sleepQuantum: %v\n", sleepQuantum)

	previousPrintTime := time.Now()
	previousFlushTime := time.Now()

	var inOffset int64 = 0
	var pastOutput pastOutputBuff
	for {
		n, err := originalFile.ReadAt(inBuff, inOffset)

		if err != nil && err != io.EOF {
			log.Fatal(err)
		}

		inRemainder := inBuff[:n]

		for currLine, inRemainder := nextLine(inRemainder); len(currLine) > 0; currLine, inRemainder = nextLine(inRemainder) {
			inOffset += int64(len(currLine))

			select {
			case linesPerSecondTarget = <-linesPerSecChan:
				if linesPerSecondTarget < MIN_LINES_PER_SECOND {
					linesPerSecondTarget = MIN_LINES_PER_SECOND
				}
				fmt.Printf("linesPerSecond: %f\n", linesPerSecondTarget)
				targetPrintPeriod = time.Nanosecond * time.Duration(1_000_000_000*1/linesPerSecondTarget)
				fmt.Printf("printPeriod: %v\n", targetPrintPeriod)
			case <-widgetOrRatesChan:
				printWidgetOrRates = !printWidgetOrRates

				fmt.Printf("printWidget (else rates): %t\n", printWidgetOrRates)
				previousPrintTime = time.Time{}
			default:
			}

			_, err2 := replayedFile.Write(currLine)
			if err2 != nil {
				log.Fatal(err2)
			}
			pastOutput.add(outputMetadata{time.Now(), len(currLine)})

			sleepToMaintainRate(targetPrintPeriod, sleepQuantum)

			// the print mechianism could be further improved:
			// Currently for low LPS rates user has to wait a long time for the first output to be print
			if printWidgetOrRates {
				if time.Since(previousPrintTime) > 30*time.Millisecond {
					printWidget(lineNo, linesPerSecondTarget)
					previousPrintTime = time.Now()
				}
			} else {
				if time.Since(previousPrintTime) > 500*time.Millisecond {
					pastOutput.printStatsFromLast(time.Second)
					previousPrintTime = time.Now()
				}
			}

			if time.Since(previousFlushTime) > time.Second {
				// flush file, update size to be visible externally
				err2 = replayedFile.Sync()
				if err2 != nil {
					log.Fatal(err2)
				}
				previousFlushTime = time.Now()
			}
			lineNo++
		}

		if err == io.EOF {
			break
		}
	}
	pastOutput.printStatsFromLast(time.Second)

	fmt.Println("\nDone")
	endOfFileChan <- true
}

func (buff *pastOutputBuff) printStatsFromLast(statsDuration time.Duration) {
	lines, bytes := buff.statsInLast(statsDuration)
	kiBytes := float32(bytes) / 1024
	fmt.Printf("%7d line/s;  %10.1f KiB/s\r", lines, kiBytes)

}

// turbocharged sleep function. When the period between line prints is shorter than the shortes possible time.Sleep() duration
// we sleep only between some of the line prints. This way the average period will be close to the desired.
func sleepToMaintainRate(desiredDuration, sleepQuantum time.Duration) {
	if desiredDuration > sleepQuantum {
		time.Sleep(desiredDuration)
		return
	}
	sleepChance := float32(desiredDuration) / float32(sleepQuantum)
	if rand.Float32() < sleepChance {
		time.Sleep(time.Nanosecond)
	}
}

// func sleepToMaintainRate_HigherPrecision(desiredDuration, sleepQuantum time.Duration) {
// 	if desiredDuration > sleepQuantum {
// 		time.Sleep(desiredDuration)
// 		return
// 	}
// 	before := time.Now()
// 	for {
// 		if time.Since(before) > desiredDuration {
// 			break
// 		}
// 	}
// }

func benchmarkSleepQuantum() time.Duration {
	var averageQuantum time.Duration
	const SAMPLES_COUNT = 10

	for i := 0; i < SAMPLES_COUNT; i++ {
		var before time.Time
		var after time.Time

		before = time.Now()
		time.Sleep(time.Nanosecond)
		after = time.Now()

		quantum := after.Sub(before)
		averageQuantum += quantum
	}

	return averageQuantum / SAMPLES_COUNT
}

func printWidget(lineNo int, desiredLinesPerSec float64) {
	Base := len(WidgetFrames)

	fmt.Printf("%s", WidgetFrames[lineNo%Base])

	digits := int(math.Log10(desiredLinesPerSec))

	for i := 0; i < digits; i++ {
		lineNo /= Base
		fmt.Printf("%s", WidgetFrames[lineNo%Base])
	}

	fmt.Printf("\r")
}
