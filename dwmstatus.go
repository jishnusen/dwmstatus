package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	bpsSign   = "b"
	kibpsSign = "kb"
	mibpsSign = "mb"

	unpluggedSign = "☢"
	pluggedSign   = "⚡"

	cpuSign = "CPU"
	memSign = "MEM"

	netReceivedSign    = "RX "
	netTransmittedSign = "TX "

	floatSeparator = ""
	dateSeparator  = ""
	fieldSeparator = " | "
)

var (
	netDevs = map[string]struct{}{
		"enp2s6:": {},
		"wlan0:":  {},
	}
	cores = runtime.NumCPU() // count of cores to scale cpu usage
	rxOld = 0
	txOld = 0
)

// fixed builds a fixed width string with given pre- and fitting suffix
func fixed(pre string, rate int) string {
	if rate < 0 {
		return pre + " ERR"
	}

	var spd = float32(rate)
	var suf = bpsSign // default: display as B/s

	switch {
	case spd >= (1000 * 1024 * 1024): // > 999 MiB/s
		return "" + pre + "ERR"
	case spd >= (1000 * 1024): // display as MiB/s
		spd /= (1024 * 1024)
		suf = mibpsSign
		pre = "" + pre + ""
	case spd >= 1000: // display as KiB/s
		spd /= 1024
		suf = kibpsSign
	}

	var formated = ""
	if spd >= 100 {
		formated = fmt.Sprintf("%3.0f", spd)
	} else if spd >= 10 {
		formated = fmt.Sprintf("%4.1f", spd)
	} else {
		formated = fmt.Sprintf(" %3.1f", spd)
	}
	return pre + strings.Replace(formated, ".", floatSeparator, 1) + suf
}

// updateNetUse reads current transfer rates of certain network interfaces
func updateNetUse() string {
	file, err := os.Open("/proc/net/dev")
	defer file.Close()
	if err != nil {
		return netReceivedSign + " ERR " + netTransmittedSign + " ERR"
	}

	var void = 0 // target for unused values
	var dev, rx, tx, rxNow, txNow = "", 0, 0, 0, 0
	var scanner = bufio.NewScanner(file)
	for scanner.Scan() {
		_, err = fmt.Sscanf(scanner.Text(), "%s %d %d %d %d %d %d %d %d %d",
			&dev, &rx, &void, &void, &void, &void, &void, &void, &void, &tx)
		if _, ok := netDevs[dev]; ok {
			rxNow += rx
			txNow += tx
		}
	}

	defer func() { rxOld, txOld = rxNow, txNow }()
	return fmt.Sprintf("%s %s", fixed(netReceivedSign, rxNow-rxOld), fixed(netTransmittedSign, txNow-txOld))
}

// colored surrounds the percentage with color escapes if it is >= 70
func colored(icon string, percentage int) string {
	if percentage >= 100 {
		return fmt.Sprintf("%s%3d", icon, percentage)
	} else if percentage >= 70 {
		return fmt.Sprintf("%s%3d", icon, percentage)
	}
	return fmt.Sprintf("%s%3d", icon, percentage)
}

// updatePower reads the current battery and power plug status
func updatePower() string {
	const powerSupply = "/sys/class/power_supply/"
	var enFull, enNow, enPerc int = 0, 0, 0
	var plugged, err = ioutil.ReadFile(powerSupply + "ADP1/online")
	if err != nil {
		return err.Error()
	}
	batts, err := ioutil.ReadDir(powerSupply)
	if err != nil {
		return err.Error()
	}

	readval := func(name, field string) int {
		var path = powerSupply + name + "/"
		var file []byte
		if tmp, err := ioutil.ReadFile(path + "energy_" + field); err == nil {
			file = tmp
		} else if tmp, err := ioutil.ReadFile(path + "charge_" + field); err == nil {
			file = tmp
		} else {
			return 0
		}

		if ret, err := strconv.Atoi(strings.TrimSpace(string(file))); err == nil {
			return ret
		}
		return 0
	}

	for _, batt := range batts {
		name := batt.Name()
		if !strings.HasPrefix(name, "BAT") {
			continue
		}

		enFull += readval(name, "full")
		enNow += readval(name, "now")
	}

	if enFull == 0 { // Battery found but no readable full file.
		return "ERR"
	}

	enPerc = enNow * 100 / enFull
	var icon = unpluggedSign
	if string(plugged) == "1\n" {
		icon = pluggedSign
	}

	if enPerc <= 5 {
		return fmt.Sprintf("%s%3d", icon, enPerc) + "%"
	} else if enPerc <= 10 {
		return fmt.Sprintf("%s%3d", icon, enPerc) + "%"
	}
	return fmt.Sprintf("%s%3d", icon, enPerc) + "%"
}

// updateCPUUse reads the last minute sysload and scales it to the core count
func updateCPUUse() string {
	var load float32
	var loadavg, err = ioutil.ReadFile("/proc/loadavg")
	if err != nil {
		return cpuSign + "ERR"
	}
	_, err = fmt.Sscanf(string(loadavg), "%f", &load)
	if err != nil {
		return cpuSign + "ERR"
	}
	return colored(cpuSign, int(load*100.0/float32(cores)))
}

// updateMemUse reads the memory used by applications and scales to [0, 100]
func updateMemUse() string {
	var file, err = os.Open("/proc/meminfo")
	defer file.Close()
	if err != nil {
		return memSign + "ERR"
	}

	// done must equal the flag combination (0001 | 0010 | 0100 | 1000) = 15
	var total, used, done = 0, 0, 0
	for info := bufio.NewScanner(file); done != 15 && info.Scan(); {
		var prop, val = "", 0
		if _, err = fmt.Sscanf(info.Text(), "%s %d", &prop, &val); err != nil {
			return memSign + "ERR"
		}
		switch prop {
		case "MemTotal:":
			total = val
			used += val
			done |= 1
		case "MemFree:":
			used -= val
			done |= 2
		case "Buffers:":
			used -= val
			done |= 4
		case "Cached:":
			used -= val
			done |= 8
		}
	}
	return colored(memSign, used*100/total)
}

func IsEmpty(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	// read in ONLY one file
	_, err = f.Readdir(1)

	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true
	}
	return false
}

// main updates the dwm statusbar every second
func main() {
	var status = []string{}
	for {
		if !IsEmpty("/sys/class/power_supply/") {
			status = []string{
				updateNetUse(),
				updateCPUUse() + "%",
				updateMemUse() + "%",
				updatePower(),
				time.Now().Local().Format("Monday January 02  3:04:05 PM"),
			}
		} else {
			status = []string{
				updateNetUse(),
				updateCPUUse() + "%",
				updateMemUse() + "%",
				time.Now().Local().Format("Monday January 02  3:04:05 PM"),
			}
		}
		exec.Command("xsetroot", "-name", strings.Join(status, fieldSeparator)).Run()

		// sleep until beginning of next second
		var now = time.Now()
		time.Sleep(now.Truncate(time.Second).Add(time.Second).Sub(now))
	}
}
