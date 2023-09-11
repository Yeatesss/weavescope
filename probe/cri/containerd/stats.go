package containerd

import (
	"context"
	"errors"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/containerd/nerdctl/pkg/statsutil"

	"github.com/coocood/freecache"

	"github.com/containerd/containerd/api/types"

	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/namespaces"
	"github.com/dolthub/swiss"
	log "github.com/sirupsen/logrus"
)

var CtrStatsLister = NewCtrStats(&containerd.Client{})

// ContainerStatsManage 容器性能数据维护结构体
type ContainerStatsManage struct {
	client     *containerd.Client
	NsLock     sync.RWMutex
	Namespaces []string
	nsChan     chan *NsChan         //新增命名空间和删除命名空间监控交流通道，避免并发行为
	Stats      map[string]*AllStats //map[namespace]all container stats
}

type NsChan struct {
	Namespace string
	Remove    bool
	Stats     chan *AllStats
}

type AllStats struct {
	ctx       context.Context
	ctxCancel context.CancelFunc
	statChan  chan *StatChan //针对于某命名空间下容器的性能监控状态维护
	stats     *swiss.Map[string, statsutil.StatsEntry]
}

type StatChan struct {
	ContainerID string
	Action      string
	Stats       statsutil.StatsEntry      //新增时候传递的性能值
	StatsRes    chan statsutil.StatsEntry //获取时候通过该channel返回
}

func NewAddCtrStats(containerID string, stats statsutil.StatsEntry) *StatChan {
	return &StatChan{
		ContainerID: containerID,
		Action:      "add",
		Stats:       stats,
		StatsRes:    make(chan statsutil.StatsEntry, 1),
	}
}
func NewGetCtrStats(containerID string) *StatChan {
	return &StatChan{
		ContainerID: containerID,
		Action:      "get",
		StatsRes:    make(chan statsutil.StatsEntry, 1),
	}
}
func NewDelCtrStats(containerID string) *StatChan {
	return &StatChan{
		ContainerID: containerID,
		Action:      "delete",
	}
}
func (c *AllStats) PushTask(manage *StatChan) {
	defer func() {
		if err := recover(); err != nil {
			log.Error("Namespace not's exists")
			if manage.StatsRes != nil {
				close(manage.StatsRes)
			}
		}
	}()
	c.statChan <- manage
}
func NewCtrStats(client *containerd.Client) *ContainerStatsManage {
	ctrStats := &ContainerStatsManage{
		client:     client,
		NsLock:     sync.RWMutex{},
		Namespaces: []string{},
		nsChan:     make(chan *NsChan, 20),
		Stats:      make(map[string]*AllStats),
	}
	go statNamespaceManage(ctrStats)
	go func() {
		for {
			for _, ns := range ctrStats.Namespaces {
				//遍历当前所有命名空间，获取命名空间下面容器的性能信息
				var (
					stats *AllStats
					wg    sync.WaitGroup
				)
				//获取命名空间下所有的容器stats
				wg.Add(1)
				go func() {
					defer wg.Done()
					var statsRes = make(chan *AllStats, 2)
					ctrStats.nsChan <- &NsChan{
						Namespace: ns,
						Stats:     statsRes,
					}
					stats = <-statsRes
				}()
				//获取容器性能数据
				ns := namespaces.WithNamespace(context.Background(), ns)
				metrics, merr := client.TaskService().Metrics(ns, &tasks.MetricsRequest{})
				if merr != nil {
					log.Errorf("failed to get metrics for namespace %s: %v", ns, merr)
					continue
				}

				if len(metrics.Metrics) == 0 {
					continue
				}
				wg.Wait()
				rangeMetrics := func() {
					var containerdStats = make(map[string]statsutil.StatsEntry, len(metrics.Metrics))
					for _, metric := range metrics.Metrics {
						containerdStats[metric.ID] = MetricToStatsEntry(metric)
					}
					for containerID, s := range containerdStats {
						stats.PushTask(
							NewAddCtrStats(containerID, s))
					}
				}
				rangeMetrics()
			}
			time.Sleep(500 * time.Millisecond)
		}

	}()
	return ctrStats
}
func statNamespaceManage(containerStats *ContainerStatsManage) {
	// 监听对命名空间进行管理，对命名空间进行删除、新增就对该命名空间下容器性能信息进行维护
	for ctrStatsManage := range containerStats.nsChan {
		//删除命名空间
		doManage := func() {
			defer func() {
				if ctrStatsManage.Stats != nil {
					close(ctrStatsManage.Stats)
				}
			}()
			stats, ok := containerStats.Stats[ctrStatsManage.Namespace]
			if ctrStatsManage.Remove {
				if ok {
					stats.stats.Clear()
					stats.ctxCancel() //取消当前的上下文
					delete(containerStats.Stats, ctrStatsManage.Namespace)
					containerStats.NsLock.Lock()
					for idx, namespace := range containerStats.Namespaces {
						if namespace == ctrStatsManage.Namespace {
							containerStats.Namespaces = append(containerStats.Namespaces[:idx], containerStats.Namespaces[idx+1:]...)
						}
					}
					containerStats.NsLock.Unlock()
				}
				return
			}
			if !ok {
				ctx, cancel := context.WithCancel(context.Background())
				stats = &AllStats{statChan: make(chan *StatChan, 20), ctx: ctx, ctxCancel: cancel, stats: swiss.NewMap[string, statsutil.StatsEntry](42)}
				go stats.statManage()
				containerStats.Stats[ctrStatsManage.Namespace] = stats
				containerStats.NsLock.Lock()
				containerStats.Namespaces = append(containerStats.Namespaces, ctrStatsManage.Namespace)
				containerStats.NsLock.Unlock()
			}
			ctrStatsManage.Stats <- stats
		}
		doManage()

	}
}
func (c *AllStats) statManage() {
	myTimer := time.NewTimer(500 * time.Millisecond) // 启动定时器
	for {
		select {
		case <-myTimer.C:
		case defineStat := <-c.statChan:
			doManage := func() {
				defer func() {
					if defineStat.StatsRes != nil {
						close(defineStat.StatsRes)
					}
				}()
				preStat, ok := c.stats.Get(defineStat.ContainerID)
				switch defineStat.Action {
				case "add":
					if ok {
						preStat.Memory = defineStat.Stats.Memory
						preStat.MemoryLimit = defineStat.Stats.MemoryLimit
						preStat.CPUPercentage = defineStat.Stats.CPUPercentage
					} else {
						preStat = defineStat.Stats
					}
					c.stats.Put(defineStat.ContainerID, preStat)
					if defineStat.StatsRes != nil {
						defineStat.StatsRes <- preStat
					}

					return
				case "get":
					if !ok {
						preStat = statsutil.StatsEntry{}
						c.stats.Put(defineStat.ContainerID, preStat)
					}
					if defineStat.StatsRes != nil {
						defineStat.StatsRes <- preStat
					}
					return
				case "delete":
					if ok {
						c.stats.Delete(defineStat.ContainerID)
						return
					}
				}
			}
			doManage()

		case <-c.ctx.Done():
			log.Error("allstats ctx cancel")
			close(c.statChan)
			c.stats.Clear()
			return
		}
		myTimer.Reset(time.Second * 1)
	}

}

func MetricUnmarsh[T any](metric typeurl.Any) (*T, error) {
	var r T
	err := typeurl.UnmarshalTo(metric, &r)
	return &r, err

}

var preStats = freecache.NewCache(1024 * 1024 * 10)

func MetricToStatsEntry(metric *types.Metric) (statsEntry statsutil.StatsEntry) {
	var (
		stat statsutil.ContainerStats
	)
	data := metric.Data
	switch {
	case typeurl.Is(data, (*v1.Metrics)(nil)):
		res, err := MetricUnmarsh[v1.Metrics](data)
		if err != nil {
			log.Errorf("failed unmarsh v1 metrics: %v", err)
			return
		}
		preStatsByte, err := preStats.Get([]byte(metric.ID))
		if !errors.Is(err, freecache.ErrNotFound) {
			var serr error
			jsoniter.Unmarshal(preStatsByte, &stat)
			statsEntry, serr = statsutil.SetCgroupStatsFields(&stat, res, nil)
			if serr != nil {
				return
			}
		}

		stat = statsutil.ContainerStats{
			CgroupCPU:    res.CPU.Usage.Total,
			CgroupSystem: res.CPU.Usage.Kernel,
		}
	case typeurl.Is(data, (*v2.Metrics)(nil)):
		res, err := MetricUnmarsh[v2.Metrics](data)
		if err != nil {
			log.Errorf("failed unmarsh v2 metrics: %v", err)
			return
		}
		preStatsByte, err := preStats.Get([]byte(metric.ID))
		if !errors.Is(err, freecache.ErrNotFound) {
			var serr error
			jsoniter.Unmarshal(preStatsByte, &stat)
			statsEntry, serr = statsutil.SetCgroup2StatsFields(&stat, res, nil)
			if serr != nil {
				return
			}
		}
		stat = statsutil.ContainerStats{
			Cgroup2CPU:    res.CPU.UsageUsec * 1000,
			Cgroup2System: res.CPU.SystemUsec * 1000,
		}

	}
	stat.Time = time.Now()
	preStatsByte, _ := jsoniter.Marshal(stat)
	preStats.Set([]byte(metric.ID), preStatsByte, 120)
	return
}

//
// func (c *container) memoryUsageMetric(stats []docker.Stats) report.Metric {
