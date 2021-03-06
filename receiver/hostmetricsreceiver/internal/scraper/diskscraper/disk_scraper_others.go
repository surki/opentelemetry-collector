// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build !windows

package diskscraper

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"

	"go.opentelemetry.io/collector/consumer/pdata"
	"go.opentelemetry.io/collector/internal/processor/filterset"
)

// scraper for Disk Metrics
type scraper struct {
	config    *Config
	startTime pdata.TimestampUnixNano
	includeFS filterset.FilterSet
	excludeFS filterset.FilterSet

	// for mocking
	bootTime   func() (uint64, error)
	ioCounters func(names ...string) (map[string]disk.IOCountersStat, error)
}

// newDiskScraper creates a Disk Scraper
func newDiskScraper(_ context.Context, cfg *Config) (*scraper, error) {
	scraper := &scraper{config: cfg, bootTime: host.BootTime, ioCounters: disk.IOCounters}

	var err error

	if len(cfg.Include.Devices) > 0 {
		scraper.includeFS, err = filterset.CreateFilterSet(cfg.Include.Devices, &cfg.Include.Config)
		if err != nil {
			return nil, fmt.Errorf("error creating device include filters: %w", err)
		}
	}

	if len(cfg.Exclude.Devices) > 0 {
		scraper.excludeFS, err = filterset.CreateFilterSet(cfg.Exclude.Devices, &cfg.Exclude.Config)
		if err != nil {
			return nil, fmt.Errorf("error creating device exclude filters: %w", err)
		}
	}

	return scraper, nil
}

// Initialize
func (s *scraper) Initialize(_ context.Context) error {
	bootTime, err := s.bootTime()
	if err != nil {
		return err
	}

	s.startTime = pdata.TimestampUnixNano(bootTime * 1e9)
	return nil
}

// Close
func (s *scraper) Close(_ context.Context) error {
	return nil
}

// ScrapeMetrics
func (s *scraper) ScrapeMetrics(_ context.Context) (pdata.MetricSlice, error) {
	metrics := pdata.NewMetricSlice()

	ioCounters, err := s.ioCounters()
	if err != nil {
		return metrics, err
	}

	// filter devices by name
	ioCounters = s.filterByDevice(ioCounters)

	if len(ioCounters) > 0 {
		metrics.Resize(3 + systemSpecificMetricsLen)
		initializeDiskIOMetric(metrics.At(0), s.startTime, ioCounters)
		initializeDiskOpsMetric(metrics.At(1), s.startTime, ioCounters)
		initializeDiskTimeMetric(metrics.At(2), s.startTime, ioCounters)
		appendSystemSpecificMetrics(metrics, 3, s.startTime, ioCounters)
	}

	return metrics, nil
}

func initializeDiskIOMetric(metric pdata.Metric, startTime pdata.TimestampUnixNano, ioCounters map[string]disk.IOCountersStat) {
	diskIODescriptor.CopyTo(metric.MetricDescriptor())

	idps := metric.Int64DataPoints()
	idps.Resize(2 * len(ioCounters))

	idx := 0
	for device, ioCounter := range ioCounters {
		initializeDataPoint(idps.At(idx+0), startTime, device, readDirectionLabelValue, int64(ioCounter.ReadBytes))
		initializeDataPoint(idps.At(idx+1), startTime, device, writeDirectionLabelValue, int64(ioCounter.WriteBytes))
		idx += 2
	}
}

func initializeDiskOpsMetric(metric pdata.Metric, startTime pdata.TimestampUnixNano, ioCounters map[string]disk.IOCountersStat) {
	diskOpsDescriptor.CopyTo(metric.MetricDescriptor())

	idps := metric.Int64DataPoints()
	idps.Resize(2 * len(ioCounters))

	idx := 0
	for device, ioCounter := range ioCounters {
		initializeDataPoint(idps.At(idx+0), startTime, device, readDirectionLabelValue, int64(ioCounter.ReadCount))
		initializeDataPoint(idps.At(idx+1), startTime, device, writeDirectionLabelValue, int64(ioCounter.WriteCount))
		idx += 2
	}
}

func initializeDiskTimeMetric(metric pdata.Metric, startTime pdata.TimestampUnixNano, ioCounters map[string]disk.IOCountersStat) {
	diskTimeDescriptor.CopyTo(metric.MetricDescriptor())

	idps := metric.Int64DataPoints()
	idps.Resize(2 * len(ioCounters))

	idx := 0
	for device, ioCounter := range ioCounters {
		initializeDataPoint(idps.At(idx+0), startTime, device, readDirectionLabelValue, int64(ioCounter.ReadTime))
		initializeDataPoint(idps.At(idx+1), startTime, device, writeDirectionLabelValue, int64(ioCounter.WriteTime))
		idx += 2
	}
}

func initializeDataPoint(dataPoint pdata.Int64DataPoint, startTime pdata.TimestampUnixNano, deviceLabel string, directionLabel string, value int64) {
	labelsMap := dataPoint.LabelsMap()
	labelsMap.Insert(deviceLabelName, deviceLabel)
	labelsMap.Insert(directionLabelName, directionLabel)
	dataPoint.SetStartTime(startTime)
	dataPoint.SetTimestamp(pdata.TimestampUnixNano(uint64(time.Now().UnixNano())))
	dataPoint.SetValue(value)
}

func (s *scraper) filterByDevice(ioCounters map[string]disk.IOCountersStat) map[string]disk.IOCountersStat {
	if s.includeFS == nil && s.excludeFS == nil {
		return ioCounters
	}

	for device := range ioCounters {
		if !s.includeDevice(device) {
			delete(ioCounters, device)
		}
	}
	return ioCounters
}

func (s *scraper) includeDevice(deviceName string) bool {
	return (s.includeFS == nil || s.includeFS.Matches(deviceName)) &&
		(s.excludeFS == nil || !s.excludeFS.Matches(deviceName))
}
