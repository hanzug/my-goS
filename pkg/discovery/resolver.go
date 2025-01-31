package discovery

import (
	"context"
	"go.uber.org/zap"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"
)

const (
	schema = "etcd" // 定义使用的schema名称，这里是etcd
)

// Resolver 结构体，用于gRPC服务的动态解析和地址更新
type Resolver struct {
	schema      string   // 解析器使用的schema
	EtcdAddrs   []string // etcd服务的地址列表
	DialTimeout int      // 连接etcd的超时时间（秒）

	closeCh      chan struct{}      // 用于关闭watch协程的通道
	watchCh      clientv3.WatchChan // etcd的watch通道
	cli          *clientv3.Client   // etcd客户端
	keyPrifix    string             // etcd中用于查找服务的键前缀
	srvAddrsList []resolver.Address // 服务地址列表

	cc resolver.ClientConn // gRPC客户端连接，用于更新服务地址
}

// NewResolver 返回etcd解析器实例
func NewResolver(etcdAddrs []string) *Resolver {
	return &Resolver{
		schema:      schema,
		EtcdAddrs:   etcdAddrs,
		DialTimeout: 3,
	}
}

func (r *Resolver) Scheme() string {
	return r.schema
}

// Build 创建一个新的resolver.Resolver
func (r *Resolver) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r.cc = cc
	r.keyPrifix = BuildPrefix(Server{Name: target.Endpoint(), Version: target.URL.Host})
	if _, err := r.start(); err != nil {
		return nil, err
	}
	return r, nil
}

// ResolveNow 是resolver.Resolver接口的一部分，用于立即解析请求
func (r *Resolver) ResolveNow(o resolver.ResolveNowOptions) {}

// Close 是resolver.Resolver接口的一部分，用于关闭解析器
func (r *Resolver) Close() {
	r.closeCh <- struct{}{}
}

// start 启动解析器
func (r *Resolver) start() (chan<- struct{}, error) {
	var err error
	r.cli, err = clientv3.New(clientv3.Config{
		Endpoints:   r.EtcdAddrs,
		DialTimeout: time.Duration(r.DialTimeout) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	resolver.Register(r)

	r.closeCh = make(chan struct{})

	if err = r.sync(); err != nil {
		return nil, err
	}

	go r.watch()

	return r.closeCh, nil
}

// watch 监听etcd中的更新事件
func (r *Resolver) watch() {
	ticker := time.NewTicker(time.Minute)
	r.watchCh = r.cli.Watch(context.Background(), r.keyPrifix, clientv3.WithPrefix())

	for {
		select {
		case <-r.closeCh:
			return
		case res, ok := <-r.watchCh:
			if ok {
				r.update(res.Events)
			}
		case <-ticker.C: // 定时同步地址到本地
			if err := r.sync(); err != nil {
				zap.S().Error("sync failed", err)
			}
		}
	}
}

// update 处理etcd事件，更新服务地址
func (r *Resolver) update(events []*clientv3.Event) {
	for _, ev := range events {
		var info Server
		var err error

		switch ev.Type {
		case clientv3.EventTypePut:
			info, err = ParseValue(ev.Kv.Value)
			if err != nil {
				continue
			}
			addr := resolver.Address{Addr: info.Addr, Metadata: info.Weight}
			if !Exist(r.srvAddrsList, addr) {
				r.srvAddrsList = append(r.srvAddrsList, addr)
				_ = r.cc.UpdateState(resolver.State{Addresses: r.srvAddrsList})
			}
		case clientv3.EventTypeDelete:
			info, err = SplitPath(string(ev.Kv.Key))
			if err != nil {
				continue
			}
			addr := resolver.Address{Addr: info.Addr}
			if s, ok := Remove(r.srvAddrsList, addr); ok {
				r.srvAddrsList = s
				_ = r.cc.UpdateState(resolver.State{Addresses: r.srvAddrsList})
			}
		}
	}
}

// sync 同步获取所有地址信息
func (r *Resolver) sync() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := r.cli.Get(ctx, r.keyPrifix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	r.srvAddrsList = []resolver.Address{}

	for _, v := range res.Kvs {
		info, err := ParseValue(v.Value)
		if err != nil {
			continue
		}
		addr := resolver.Address{Addr: info.Addr, Metadata: info.Weight}
		r.srvAddrsList = append(r.srvAddrsList, addr)
	}
	return r.cc.UpdateState(resolver.State{Addresses: r.srvAddrsList})
}
