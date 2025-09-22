# logreplay

A tool to copy given logfile at a given rate (lines per second). Useful for simulating actual programs outputting actual logs, and for testing programs that process live logs.


## Build

From the repo root run

    go build .

## Usage
Run the program with

    logreplay original.log replayed.log

The above will copy file `original.log` to `replayed.log` at a slow rate and display live summary similar to the one below:

    9 line/s;         1.2 KiB/s

The rate at which log is replayed can be set by the user. At any point just type the required lines per second and press Enter. Eg. Type 10000 to copy at 10000 lines/second.


Instead of numbers, an animated widget can be used to illustrate current print rate.

![Line per second widget](https://raw.githubusercontent.com/macsmol/logreplay/refs/heads/main/linePerSecRateWidget.gif)

 At any point press `t` and press Enter to toggle between rates and widget.