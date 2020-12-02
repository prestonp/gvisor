// Copyright 2019 The gVisor Authors.
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

package ipv6

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	// defaultRetransmitTimer is the default amount of time to wait between
	// sending reachability probes.
	//
	// Default taken from RETRANS_TIMER of RFC 4861 section 10.
	defaultRetransmitTimer = time.Second

	// minimumRetransmitTimer is the minimum amount of time to wait between
	// sending reachability probes.
	//
	// Note, RFC 4861 does not impose a minimum Retransmit Timer, but we do here
	// to make sure the messages are not sent all at once. We also come to this
	// value because in the RetransmitTimer field of a Router Advertisement, a
	// value of 0 means unspecified, so the smallest valid value is 1. Note, the
	// unit of the RetransmitTimer field in the Router Advertisement is
	// milliseconds.
	minimumRetransmitTimer = time.Millisecond

	// defaultDupAddrDetectTransmits is the default number of NDP Neighbor
	// Solicitation messages to send when doing Duplicate Address Detection
	// for a tentative address.
	//
	// Default = 1 (from RFC 4862 section 5.1)
	defaultDupAddrDetectTransmits = 1

	// defaultMaxRtrSolicitations is the default number of Router
	// Solicitation messages to send when an IPv6 endpoint becomes enabled.
	//
	// Default = 3 (from RFC 4861 section 10).
	defaultMaxRtrSolicitations = 3

	// defaultRtrSolicitationInterval is the default amount of time between
	// sending Router Solicitation messages.
	//
	// Default = 4s (from 4861 section 10).
	defaultRtrSolicitationInterval = 4 * time.Second

	// defaultMaxRtrSolicitationDelay is the default maximum amount of time
	// to wait before sending the first Router Solicitation message.
	//
	// Default = 1s (from 4861 section 10).
	defaultMaxRtrSolicitationDelay = time.Second

	// defaultHandleRAs is the default configuration for whether or not to
	// handle incoming Router Advertisements as a host.
	defaultHandleRAs = true

	// defaultDiscoverDefaultRouters is the default configuration for
	// whether or not to discover default routers from incoming Router
	// Advertisements, as a host.
	defaultDiscoverDefaultRouters = true

	// defaultDiscoverOnLinkPrefixes is the default configuration for
	// whether or not to discover on-link prefixes from incoming Router
	// Advertisements' Prefix Information option, as a host.
	defaultDiscoverOnLinkPrefixes = true

	// defaultAutoGenGlobalAddresses is the default configuration for
	// whether or not to generate global IPv6 addresses in response to
	// receiving a new Prefix Information option with its Autonomous
	// Address AutoConfiguration flag set, as a host.
	//
	// Default = true.
	defaultAutoGenGlobalAddresses = true

	// minimumRtrSolicitationInterval is the minimum amount of time to wait
	// between sending Router Solicitation messages. This limit is imposed
	// to make sure that Router Solicitation messages are not sent all at
	// once, defeating the purpose of sending the initial few messages.
	minimumRtrSolicitationInterval = 500 * time.Millisecond

	// minimumMaxRtrSolicitationDelay is the minimum amount of time to wait
	// before sending the first Router Solicitation message. It is 0 because
	// we cannot have a negative delay.
	minimumMaxRtrSolicitationDelay = 0

	// MaxDiscoveredDefaultRouters is the maximum number of discovered
	// default routers. The stack should stop discovering new routers after
	// discovering MaxDiscoveredDefaultRouters routers.
	//
	// This value MUST be at minimum 2 as per RFC 4861 section 6.3.4, and
	// SHOULD be more.
	MaxDiscoveredDefaultRouters = 10

	// MaxDiscoveredOnLinkPrefixes is the maximum number of discovered
	// on-link prefixes. The stack should stop discovering new on-link
	// prefixes after discovering MaxDiscoveredOnLinkPrefixes on-link
	// prefixes.
	MaxDiscoveredOnLinkPrefixes = 10

	// validPrefixLenForAutoGen is the expected prefix length that an
	// address can be generated for. Must be 64 bits as the interface
	// identifier (IID) is 64 bits and an IPv6 address is 128 bits, so
	// 128 - 64 = 64.
	validPrefixLenForAutoGen = 64

	// defaultAutoGenTempGlobalAddresses is the default configuration for whether
	// or not to generate temporary SLAAC addresses.
	defaultAutoGenTempGlobalAddresses = true

	// defaultMaxTempAddrValidLifetime is the default maximum valid lifetime
	// for temporary SLAAC addresses generated as part of RFC 4941.
	//
	// Default = 7 days (from RFC 4941 section 5).
	defaultMaxTempAddrValidLifetime = 7 * 24 * time.Hour

	// defaultMaxTempAddrPreferredLifetime is the default preferred lifetime
	// for temporary SLAAC addresses generated as part of RFC 4941.
	//
	// Default = 1 day (from RFC 4941 section 5).
	defaultMaxTempAddrPreferredLifetime = 24 * time.Hour

	// defaultRegenAdvanceDuration is the default duration before the deprecation
	// of a temporary address when a new address will be generated.
	//
	// Default = 5s (from RFC 4941 section 5).
	defaultRegenAdvanceDuration = 5 * time.Second

	// minRegenAdvanceDuration is the minimum duration before the deprecation
	// of a temporary address when a new address will be generated.
	minRegenAdvanceDuration = time.Duration(0)

	// maxSLAACAddrLocalRegenAttempts is the maximum number of times to attempt
	// SLAAC address regenerations in response to an IPv6 endpoint-local conflict.
	maxSLAACAddrLocalRegenAttempts = 10
)

var (
	// MinPrefixInformationValidLifetimeForUpdate is the minimum Valid
	// Lifetime to update the valid lifetime of a generated address by
	// SLAAC.
	//
	// This is exported as a variable (instead of a constant) so tests
	// can update it to a smaller value.
	//
	// Min = 2hrs.
	MinPrefixInformationValidLifetimeForUpdate = 2 * time.Hour

	// MaxDesyncFactor is the upper bound for the preferred lifetime's desync
	// factor for temporary SLAAC addresses.
	//
	// This is exported as a variable (instead of a constant) so tests
	// can update it to a smaller value.
	//
	// Must be greater than 0.
	//
	// Max = 10m (from RFC 4941 section 5).
	MaxDesyncFactor = 10 * time.Minute

	// MinMaxTempAddrPreferredLifetime is the minimum value allowed for the
	// maximum preferred lifetime for temporary SLAAC addresses.
	//
	// This is exported as a variable (instead of a constant) so tests
	// can update it to a smaller value.
	//
	// This value guarantees that a temporary address is preferred for at
	// least 1hr if the SLAAC prefix is valid for at least that time.
	MinMaxTempAddrPreferredLifetime = defaultRegenAdvanceDuration + MaxDesyncFactor + time.Hour

	// MinMaxTempAddrValidLifetime is the minimum value allowed for the
	// maximum valid lifetime for temporary SLAAC addresses.
	//
	// This is exported as a variable (instead of a constant) so tests
	// can update it to a smaller value.
	//
	// This value guarantees that a temporary address is valid for at least
	// 2hrs if the SLAAC prefix is valid for at least that time.
	MinMaxTempAddrValidLifetime = 2 * time.Hour
)

// NDPEndpoint is an endpoint that supports NDP.
type NDPEndpoint interface {
	// SetNDPConfigurations sets the NDP configurations.
	SetNDPConfigurations(NDPConfigurations)
}

// DHCPv6ConfigurationFromNDPRA is a configuration available via DHCPv6 that an
// NDP Router Advertisement informed the Stack about.
type DHCPv6ConfigurationFromNDPRA int

const (
	_ DHCPv6ConfigurationFromNDPRA = iota

	// DHCPv6NoConfiguration indicates that no configurations are available via
	// DHCPv6.
	DHCPv6NoConfiguration

	// DHCPv6ManagedAddress indicates that addresses are available via DHCPv6.
	//
	// DHCPv6ManagedAddress also implies DHCPv6OtherConfigurations because DHCPv6
	// returns all available configuration information when serving addresses.
	DHCPv6ManagedAddress

	// DHCPv6OtherConfigurations indicates that other configuration information is
	// available via DHCPv6.
	//
	// Other configurations are configurations other than addresses. Examples of
	// other configurations are recursive DNS server list, DNS search lists and
	// default gateway.
	DHCPv6OtherConfigurations
)

// NDPDispatcher is the interface integrators of netstack must implement to
// receive and handle NDP related events.
type NDPDispatcher interface {
	// OnDuplicateAddressDetectionStatus is called when the DAD process for an
	// address (addr) on a NIC (with ID nicID) completes. resolved is set to true
	// if DAD completed successfully (no duplicate addr detected); false otherwise
	// (addr was detected to be a duplicate on the link the NIC is a part of, or
	// it was stopped for some other reason, such as the address being removed).
	// If an error occured during DAD, err is set and resolved must be ignored.
	//
	// This function is not permitted to block indefinitely. This function
	// is also not permitted to call into the stack.
	OnDuplicateAddressDetectionStatus(nicID tcpip.NICID, addr tcpip.Address, resolved bool, err *tcpip.Error)

	// OnDefaultRouterDiscovered is called when a new default router is
	// discovered. Implementations must return true if the newly discovered
	// router should be remembered.
	//
	// This function is not permitted to block indefinitely. This function
	// is also not permitted to call into the stack.
	OnDefaultRouterDiscovered(nicID tcpip.NICID, addr tcpip.Address) bool

	// OnDefaultRouterInvalidated is called when a discovered default router that
	// was remembered is invalidated.
	//
	// This function is not permitted to block indefinitely. This function
	// is also not permitted to call into the stack.
	OnDefaultRouterInvalidated(nicID tcpip.NICID, addr tcpip.Address)

	// OnOnLinkPrefixDiscovered is called when a new on-link prefix is discovered.
	// Implementations must return true if the newly discovered on-link prefix
	// should be remembered.
	//
	// This function is not permitted to block indefinitely. This function
	// is also not permitted to call into the stack.
	OnOnLinkPrefixDiscovered(nicID tcpip.NICID, prefix tcpip.Subnet) bool

	// OnOnLinkPrefixInvalidated is called when a discovered on-link prefix that
	// was remembered is invalidated.
	//
	// This function is not permitted to block indefinitely. This function
	// is also not permitted to call into the stack.
	OnOnLinkPrefixInvalidated(nicID tcpip.NICID, prefix tcpip.Subnet)

	// OnAutoGenAddress is called when a new prefix with its autonomous address-
	// configuration flag set is received and SLAAC was performed. Implementations
	// may prevent the stack from assigning the address to the NIC by returning
	// false.
	//
	// This function is not permitted to block indefinitely. It must not
	// call functions on the stack itself.
	OnAutoGenAddress(tcpip.NICID, tcpip.AddressWithPrefix) bool

	// OnAutoGenAddressDeprecated is called when an auto-generated address (SLAAC)
	// is deprecated, but is still considered valid. Note, if an address is
	// invalidated at the same ime it is deprecated, the deprecation event may not
	// be received.
	//
	// This function is not permitted to block indefinitely. It must not
	// call functions on the stack itself.
	OnAutoGenAddressDeprecated(tcpip.NICID, tcpip.AddressWithPrefix)

	// OnAutoGenAddressInvalidated is called when an auto-generated address
	// (SLAAC) is invalidated.
	//
	// This function is not permitted to block indefinitely. It must not
	// call functions on the stack itself.
	OnAutoGenAddressInvalidated(tcpip.NICID, tcpip.AddressWithPrefix)

	// OnRecursiveDNSServerOption is called when the stack learns of DNS servers
	// through NDP. Note, the addresses may contain link-local addresses.
	//
	// It is up to the caller to use the DNS Servers only for their valid
	// lifetime. OnRecursiveDNSServerOption may be called for new or
	// already known DNS servers. If called with known DNS servers, their
	// valid lifetimes must be refreshed to lifetime (it may be increased,
	// decreased, or completely invalidated when lifetime = 0).
	//
	// This function is not permitted to block indefinitely. It must not
	// call functions on the stack itself.
	OnRecursiveDNSServerOption(nicID tcpip.NICID, addrs []tcpip.Address, lifetime time.Duration)

	// OnDNSSearchListOption is called when the stack learns of DNS search lists
	// through NDP.
	//
	// It is up to the caller to use the domain names in the search list
	// for only their valid lifetime. OnDNSSearchListOption may be called
	// with new or already known domain names. If called with known domain
	// names, their valid lifetimes must be refreshed to lifetime (it may
	// be increased, decreased or completely invalidated when lifetime = 0.
	OnDNSSearchListOption(nicID tcpip.NICID, domainNames []string, lifetime time.Duration)

	// OnDHCPv6Configuration is called with an updated configuration that is
	// available via DHCPv6 for the passed NIC.
	//
	// This function is not permitted to block indefinitely. It must not
	// call functions on the stack itself.
	OnDHCPv6Configuration(tcpip.NICID, DHCPv6ConfigurationFromNDPRA)
}

// NDPConfigurations is the NDP configurations for the netstack.
type NDPConfigurations struct {
	// The number of Neighbor Solicitation messages to send when doing
	// Duplicate Address Detection for a tentative address.
	//
	// Note, a value of zero effectively disables DAD.
	DupAddrDetectTransmits uint8

	// The amount of time to wait between sending Neighbor solicitation
	// messages.
	//
	// Must be greater than or equal to 1ms.
	RetransmitTimer time.Duration

	// The number of Router Solicitation messages to send when the IPv6 endpoint
	// becomes enabled.
	MaxRtrSolicitations uint8

	// The amount of time between transmitting Router Solicitation messages.
	//
	// Must be greater than or equal to 0.5s.
	RtrSolicitationInterval time.Duration

	// The maximum amount of time before transmitting the first Router
	// Solicitation message.
	//
	// Must be greater than or equal to 0s.
	MaxRtrSolicitationDelay time.Duration

	// HandleRAs determines whether or not Router Advertisements are processed.
	HandleRAs bool

	// DiscoverDefaultRouters determines whether or not default routers are
	// discovered from Router Advertisements, as per RFC 4861 section 6. This
	// configuration is ignored if HandleRAs is false.
	DiscoverDefaultRouters bool

	// DiscoverOnLinkPrefixes determines whether or not on-link prefixes are
	// discovered from Router Advertisements' Prefix Information option, as per
	// RFC 4861 section 6. This configuration is ignored if HandleRAs is false.
	DiscoverOnLinkPrefixes bool

	// AutoGenGlobalAddresses determines whether or not an IPv6 endpoint performs
	// SLAAC to auto-generate global SLAAC addresses in response to Prefix
	// Information options, as per RFC 4862.
	//
	// Note, if an address was already generated for some unique prefix, as
	// part of SLAAC, this option does not affect whether or not the
	// lifetime(s) of the generated address changes; this option only
	// affects the generation of new addresses as part of SLAAC.
	AutoGenGlobalAddresses bool

	// AutoGenAddressConflictRetries determines how many times to attempt to retry
	// generation of a permanent auto-generated address in response to DAD
	// conflicts.
	//
	// If the method used to generate the address does not support creating
	// alternative addresses (e.g. IIDs based on the modified EUI64 of a NIC's
	// MAC address), then no attempt is made to resolve the conflict.
	AutoGenAddressConflictRetries uint8

	// AutoGenTempGlobalAddresses determines whether or not temporary SLAAC
	// addresses are generated for an IPv6 endpoint as part of SLAAC privacy
	// extensions, as per RFC 4941.
	//
	// Ignored if AutoGenGlobalAddresses is false.
	AutoGenTempGlobalAddresses bool

	// MaxTempAddrValidLifetime is the maximum valid lifetime for temporary
	// SLAAC addresses.
	MaxTempAddrValidLifetime time.Duration

	// MaxTempAddrPreferredLifetime is the maximum preferred lifetime for
	// temporary SLAAC addresses.
	MaxTempAddrPreferredLifetime time.Duration

	// RegenAdvanceDuration is the duration before the deprecation of a temporary
	// address when a new address will be generated.
	RegenAdvanceDuration time.Duration
}

// DefaultNDPConfigurations returns an NDPConfigurations populated with
// default values.
func DefaultNDPConfigurations() NDPConfigurations {
	return NDPConfigurations{
		DupAddrDetectTransmits:       defaultDupAddrDetectTransmits,
		RetransmitTimer:              defaultRetransmitTimer,
		MaxRtrSolicitations:          defaultMaxRtrSolicitations,
		RtrSolicitationInterval:      defaultRtrSolicitationInterval,
		MaxRtrSolicitationDelay:      defaultMaxRtrSolicitationDelay,
		HandleRAs:                    defaultHandleRAs,
		DiscoverDefaultRouters:       defaultDiscoverDefaultRouters,
		DiscoverOnLinkPrefixes:       defaultDiscoverOnLinkPrefixes,
		AutoGenGlobalAddresses:       defaultAutoGenGlobalAddresses,
		AutoGenTempGlobalAddresses:   defaultAutoGenTempGlobalAddresses,
		MaxTempAddrValidLifetime:     defaultMaxTempAddrValidLifetime,
		MaxTempAddrPreferredLifetime: defaultMaxTempAddrPreferredLifetime,
		RegenAdvanceDuration:         defaultRegenAdvanceDuration,
	}
}

// validate modifies an NDPConfigurations with valid values. If invalid values
// are present in c, the corresponding default values are used instead.
func (c *NDPConfigurations) validate() {
	if c.RetransmitTimer < minimumRetransmitTimer {
		c.RetransmitTimer = defaultRetransmitTimer
	}

	if c.RtrSolicitationInterval < minimumRtrSolicitationInterval {
		c.RtrSolicitationInterval = defaultRtrSolicitationInterval
	}

	if c.MaxRtrSolicitationDelay < minimumMaxRtrSolicitationDelay {
		c.MaxRtrSolicitationDelay = defaultMaxRtrSolicitationDelay
	}

	if c.MaxTempAddrValidLifetime < MinMaxTempAddrValidLifetime {
		c.MaxTempAddrValidLifetime = MinMaxTempAddrValidLifetime
	}

	if c.MaxTempAddrPreferredLifetime < MinMaxTempAddrPreferredLifetime || c.MaxTempAddrPreferredLifetime > c.MaxTempAddrValidLifetime {
		c.MaxTempAddrPreferredLifetime = MinMaxTempAddrPreferredLifetime
	}

	if c.RegenAdvanceDuration < minRegenAdvanceDuration {
		c.RegenAdvanceDuration = minRegenAdvanceDuration
	}
}

// ndpState is the per-interface NDP state.
type ndpState struct {
	// The IPv6 endpoint this ndpState is for.
	ep *endpoint

	// configs is the per-interface NDP configurations.
	configs NDPConfigurations

	// The DAD state to send the next NS message, or resolve the address.
	dad map[tcpip.Address]dadState

	// The default routers discovered through Router Advertisements.
	defaultRouters map[tcpip.Address]defaultRouterState

	// The job used to send the next router solicitation message.
	rtrSolicitJob *tcpip.Job

	// The on-link prefixes discovered through Router Advertisements' Prefix
	// Information option.
	onLinkPrefixes map[tcpip.Subnet]onLinkPrefixState

	// The SLAAC prefixes discovered through Router Advertisements' Prefix
	// Information option.
	slaacPrefixes map[tcpip.Subnet]slaacPrefixState

	// The last learned DHCPv6 configuration from an NDP RA.
	dhcpv6Configuration DHCPv6ConfigurationFromNDPRA

	// temporaryIIDHistory is the history value used to generate a new temporary
	// IID.
	temporaryIIDHistory [header.IIDSize]byte

	// temporaryAddressDesyncFactor is the preferred lifetime's desync factor for
	// temporary SLAAC addresses.
	temporaryAddressDesyncFactor time.Duration
}

// dadState holds the Duplicate Address Detection timer and channel to signal
// to the DAD goroutine that DAD should stop.
type dadState struct {
	// The DAD timer to send the next NS message, or resolve the address.
	job *tcpip.Job

	// Used to let the DAD timer know that it has been stopped.
	//
	// Must only be read from or written to while protected by the lock of
	// the IPv6 endpoint this dadState is associated with.
	done *bool
}

// defaultRouterState holds data associated with a default router discovered by
// a Router Advertisement (RA).
type defaultRouterState struct {
	// Job to invalidate the default router.
	//
	// Must not be nil.
	invalidationJob *tcpip.Job
}

// onLinkPrefixState holds data associated with an on-link prefix discovered by
// a Router Advertisement's Prefix Information option (PI) when the NDP
// configurations was configured to do so.
type onLinkPrefixState struct {
	// Job to invalidate the on-link prefix.
	//
	// Must not be nil.
	invalidationJob *tcpip.Job
}

// tempSLAACAddrState holds state associated with a temporary SLAAC address.
type tempSLAACAddrState struct {
	// Job to deprecate the temporary SLAAC address.
	//
	// Must not be nil.
	deprecationJob *tcpip.Job

	// Job to invalidate the temporary SLAAC address.
	//
	// Must not be nil.
	invalidationJob *tcpip.Job

	// Job to regenerate the temporary SLAAC address.
	//
	// Must not be nil.
	regenJob *tcpip.Job

	createdAt time.Time

	// The address's endpoint.
	//
	// Must not be nil.
	addressEndpoint stack.AddressEndpoint

	// Has a new temporary SLAAC address already been regenerated?
	regenerated bool
}

// slaacPrefixState holds state associated with a SLAAC prefix.
type slaacPrefixState struct {
	// Job to deprecate the prefix.
	//
	// Must not be nil.
	deprecationJob *tcpip.Job

	// Job to invalidate the prefix.
	//
	// Must not be nil.
	invalidationJob *tcpip.Job

	// Nonzero only when the address is not valid forever.
	validUntil time.Time

	// Nonzero only when the address is not preferred forever.
	preferredUntil time.Time

	// State associated with the stable address generated for the prefix.
	stableAddr struct {
		// The address's endpoint.
		//
		// May only be nil when the address is being (re-)generated. Otherwise,
		// must not be nil as all SLAAC prefixes must have a stable address.
		addressEndpoint stack.AddressEndpoint

		// The number of times an address has been generated locally where the IPv6
		// endpoint already had the generated address.
		localGenerationFailures uint8
	}

	// The temporary (short-lived) addresses generated for the SLAAC prefix.
	tempAddrs map[tcpip.Address]tempSLAACAddrState

	// The next two fields are used by both stable and temporary addresses
	// generated for a SLAAC prefix. This is safe as only 1 address is in the
	// generation and DAD process at any time. That is, no two addresses are
	// generated at the same time for a given SLAAC prefix.

	// The number of times an address has been generated and added to the IPv6
	// endpoint.
	//
	// Addresses may be regenerated in reseponse to a DAD conflicts.
	generationAttempts uint8

	// The maximum number of times to attempt regeneration of a SLAAC address
	// in response to DAD conflicts.
	maxGenerationAttempts uint8
}

// startDuplicateAddressDetection performs Duplicate Address Detection.
//
// This function must only be called by IPv6 addresses that are currently
// tentative.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) startDuplicateAddressDetection(addr tcpip.Address, addressEndpoint stack.AddressEndpoint) *tcpip.Error {
	// addr must be a valid unicast IPv6 address.
	if !header.IsV6UnicastAddress(addr) {
		return tcpip.ErrAddressFamilyNotSupported
	}

	if addressEndpoint.GetKind() != stack.PermanentTentative {
		// The endpoint should be marked as tentative since we are starting DAD.
		panic(fmt.Sprintf("ndpdad: addr %s is not tentative on NIC(%d)", addr, ndp.ep.nic.ID()))
	}

	// Should not attempt to perform DAD on an address that is currently in the
	// DAD process.
	if _, ok := ndp.dad[addr]; ok {
		// Should never happen because we should only ever call this function for
		// newly created addresses. If we attemped to "add" an address that already
		// existed, we would get an error since we attempted to add a duplicate
		// address, or its reference count would have been increased without doing
		// the work that would have been done for an address that was brand new.
		// See endpoint.addAddressLocked.
		panic(fmt.Sprintf("ndpdad: already performing DAD for addr %s on NIC(%d)", addr, ndp.ep.nic.ID()))
	}

	remaining := ndp.configs.DupAddrDetectTransmits
	if remaining == 0 {
		addressEndpoint.SetKind(stack.Permanent)

		// Consider DAD to have resolved even if no DAD messages were actually
		// transmitted.
		if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
			ndpDisp.OnDuplicateAddressDetectionStatus(ndp.ep.nic.ID(), addr, true, nil)
		}

		return nil
	}

	state := dadState{
		job: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			state, ok := ndp.dad[addr]
			if !ok {
				panic(fmt.Sprintf("ndpdad: DAD timer fired but missing state for %s on NIC(%d)", addr, ndp.ep.nic.ID()))
			}

			if addressEndpoint.GetKind() != stack.PermanentTentative {
				// The endpoint should still be marked as tentative since we are still
				// performing DAD on it.
				panic(fmt.Sprintf("ndpdad: addr %s is no longer tentative on NIC(%d)", addr, ndp.ep.nic.ID()))
			}

			dadDone := remaining == 0

			var err *tcpip.Error
			if !dadDone {
				err = ndp.sendDADPacket(addr, addressEndpoint)
			}

			if dadDone {
				// DAD has resolved.
				addressEndpoint.SetKind(stack.Permanent)
			} else if err == nil {
				// DAD is not done and we had no errors when sending the last NDP NS,
				// schedule the next DAD timer.
				remaining--
				state.job.Schedule(ndp.configs.RetransmitTimer)
				return
			}

			// At this point we know that either DAD is done or we hit an error
			// sending the last NDP NS. Either way, clean up addr's DAD state and let
			// the integrator know DAD has completed.
			delete(ndp.dad, addr)

			if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
				ndpDisp.OnDuplicateAddressDetectionStatus(ndp.ep.nic.ID(), addr, dadDone, err)
			}

			// If DAD resolved for a stable SLAAC address, attempt generation of a
			// temporary SLAAC address.
			if dadDone && addressEndpoint.ConfigType() == stack.AddressConfigSlaac {
				// Reset the generation attempts counter as we are starting the generation
				// of a new address for the SLAAC prefix.
				ndp.regenerateTempSLAACAddr(addressEndpoint.AddressWithPrefix().Subnet(), true /* resetGenAttempts */)
			}
		}),
	}

	// We initially start a timer to fire immediately because some of the DAD work
	// cannot be done while holding the IPv6 endpoint's lock. This is effectively
	// the same as starting a goroutine but we use a timer that fires immediately
	// so we can reset it for the next DAD iteration.
	state.job.Schedule(0)
	ndp.dad[addr] = state

	return nil
}

// sendDADPacket sends a NS message to see if any nodes on ndp's NIC's link owns
// addr.
//
// addr must be a tentative IPv6 address on ndp's IPv6 endpoint.
func (ndp *ndpState) sendDADPacket(addr tcpip.Address, addressEndpoint stack.AddressEndpoint) *tcpip.Error {
	snmc := header.SolicitedNodeAddr(addr)

	icmp := header.ICMPv6(buffer.NewView(header.ICMPv6NeighborSolicitMinimumSize))
	icmp.SetType(header.ICMPv6NeighborSolicit)
	ns := header.NDPNeighborSolicit(icmp.MessageBody())
	ns.SetTargetAddress(addr)
	icmp.SetChecksum(header.ICMPv6Checksum(icmp, header.IPv6Any, snmc, buffer.VectorisedView{}))

	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		ReserveHeaderBytes: int(ndp.ep.MaxHeaderLength()),
		Data:               buffer.View(icmp).ToVectorisedView(),
	})

	sent := ndp.ep.protocol.stack.Stats().ICMP.V6.PacketsSent
	ndp.ep.addIPHeader(header.IPv6Any, snmc, pkt, stack.NetworkHeaderParams{
		Protocol: header.ICMPv6ProtocolNumber,
		TTL:      header.NDPHopLimit,
	}, nil /* extensionHeaders */)

	if err := ndp.ep.nic.WritePacketToRemote(header.EthernetAddressFromMulticastIPv6Address(snmc), nil /* gso */, ProtocolNumber, pkt); err != nil {
		sent.Dropped.Increment()
		return err
	}
	sent.NeighborSolicit.Increment()
	return nil
}

// stopDuplicateAddressDetection ends a running Duplicate Address Detection
// process. Note, this may leave the DAD process for a tentative address in
// such a state forever, unless some other external event resolves the DAD
// process (receiving an NA from the true owner of addr, or an NS for addr
// (implying another node is attempting to use addr)). It is up to the caller
// of this function to handle such a scenario.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) stopDuplicateAddressDetection(addr tcpip.Address) {
	dad, ok := ndp.dad[addr]
	if !ok {
		// Not currently performing DAD on addr, just return.
		return
	}

	dad.job.Cancel()
	delete(ndp.dad, addr)

	// Let the integrator know DAD did not resolve.
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnDuplicateAddressDetectionStatus(ndp.ep.nic.ID(), addr, false, nil)
	}
}

// handleRA handles a Router Advertisement message that arrived on the NIC
// this ndp is for. Does nothing if the NIC is configured to not handle RAs.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) handleRA(ip tcpip.Address, ra header.NDPRouterAdvert) {
	// Is the IPv6 endpoint configured to handle RAs at all?
	//
	// Currently, the stack does not determine router interface status on a
	// per-interface basis; it is a protocol-wide configuration, so we check the
	// protocol's forwarding flag to determine if the IPv6 endpoint is forwarding
	// packets.
	if !ndp.configs.HandleRAs || ndp.ep.protocol.Forwarding() {
		return
	}

	// Only worry about the DHCPv6 configuration if we have an NDPDispatcher as we
	// only inform the dispatcher on configuration changes. We do nothing else
	// with the information.
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		var configuration DHCPv6ConfigurationFromNDPRA
		switch {
		case ra.ManagedAddrConfFlag():
			configuration = DHCPv6ManagedAddress

		case ra.OtherConfFlag():
			configuration = DHCPv6OtherConfigurations

		default:
			configuration = DHCPv6NoConfiguration
		}

		if ndp.dhcpv6Configuration != configuration {
			ndp.dhcpv6Configuration = configuration
			ndpDisp.OnDHCPv6Configuration(ndp.ep.nic.ID(), configuration)
		}
	}

	// Is the IPv6 endpoint configured to discover default routers?
	if ndp.configs.DiscoverDefaultRouters {
		rtr, ok := ndp.defaultRouters[ip]
		rl := ra.RouterLifetime()
		switch {
		case !ok && rl != 0:
			// This is a new default router we are discovering.
			//
			// Only remember it if we currently know about less than
			// MaxDiscoveredDefaultRouters routers.
			if len(ndp.defaultRouters) < MaxDiscoveredDefaultRouters {
				ndp.rememberDefaultRouter(ip, rl)
			}

		case ok && rl != 0:
			// This is an already discovered default router. Update
			// the invalidation job.
			rtr.invalidationJob.Cancel()
			rtr.invalidationJob.Schedule(rl)
			ndp.defaultRouters[ip] = rtr

		case ok && rl == 0:
			// We know about the router but it is no longer to be
			// used as a default router so invalidate it.
			ndp.invalidateDefaultRouter(ip)
		}
	}

	// TODO(b/141556115): Do (RetransTimer, ReachableTime)) Parameter
	//                    Discovery.

	// We know the options is valid as far as wire format is concerned since
	// we got the Router Advertisement, as documented by this fn. Given this
	// we do not check the iterator for errors on calls to Next.
	it, _ := ra.Options().Iter(false)
	for opt, done, _ := it.Next(); !done; opt, done, _ = it.Next() {
		switch opt := opt.(type) {
		case header.NDPRecursiveDNSServer:
			if ndp.ep.protocol.options.NDPDisp == nil {
				continue
			}

			addrs, _ := opt.Addresses()
			ndp.ep.protocol.options.NDPDisp.OnRecursiveDNSServerOption(ndp.ep.nic.ID(), addrs, opt.Lifetime())

		case header.NDPDNSSearchList:
			if ndp.ep.protocol.options.NDPDisp == nil {
				continue
			}

			domainNames, _ := opt.DomainNames()
			ndp.ep.protocol.options.NDPDisp.OnDNSSearchListOption(ndp.ep.nic.ID(), domainNames, opt.Lifetime())

		case header.NDPPrefixInformation:
			prefix := opt.Subnet()

			// Is the prefix a link-local?
			if header.IsV6LinkLocalAddress(prefix.ID()) {
				// ...Yes, skip as per RFC 4861 section 6.3.4,
				// and RFC 4862 section 5.5.3.b (for SLAAC).
				continue
			}

			// Is the Prefix Length 0?
			if prefix.Prefix() == 0 {
				// ...Yes, skip as this is an invalid prefix
				// as all IPv6 addresses cannot be on-link.
				continue
			}

			if opt.OnLinkFlag() {
				ndp.handleOnLinkPrefixInformation(opt)
			}

			if opt.AutonomousAddressConfigurationFlag() {
				ndp.handleAutonomousPrefixInformation(opt)
			}
		}

		// TODO(b/141556115): Do (MTU) Parameter Discovery.
	}
}

// invalidateDefaultRouter invalidates a discovered default router.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) invalidateDefaultRouter(ip tcpip.Address) {
	rtr, ok := ndp.defaultRouters[ip]

	// Is the router still discovered?
	if !ok {
		// ...Nope, do nothing further.
		return
	}

	rtr.invalidationJob.Cancel()
	delete(ndp.defaultRouters, ip)

	// Let the integrator know a discovered default router is invalidated.
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnDefaultRouterInvalidated(ndp.ep.nic.ID(), ip)
	}
}

// rememberDefaultRouter remembers a newly discovered default router with IPv6
// link-local address ip with lifetime rl.
//
// The router identified by ip MUST NOT already be known by the IPv6 endpoint.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) rememberDefaultRouter(ip tcpip.Address, rl time.Duration) {
	ndpDisp := ndp.ep.protocol.options.NDPDisp
	if ndpDisp == nil {
		return
	}

	// Inform the integrator when we discovered a default router.
	if !ndpDisp.OnDefaultRouterDiscovered(ndp.ep.nic.ID(), ip) {
		// Informed by the integrator to not remember the router, do
		// nothing further.
		return
	}

	state := defaultRouterState{
		invalidationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			ndp.invalidateDefaultRouter(ip)
		}),
	}

	state.invalidationJob.Schedule(rl)

	ndp.defaultRouters[ip] = state
}

// rememberOnLinkPrefix remembers a newly discovered on-link prefix with IPv6
// address with prefix prefix with lifetime l.
//
// The prefix identified by prefix MUST NOT already be known.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) rememberOnLinkPrefix(prefix tcpip.Subnet, l time.Duration) {
	ndpDisp := ndp.ep.protocol.options.NDPDisp
	if ndpDisp == nil {
		return
	}

	// Inform the integrator when we discovered an on-link prefix.
	if !ndpDisp.OnOnLinkPrefixDiscovered(ndp.ep.nic.ID(), prefix) {
		// Informed by the integrator to not remember the prefix, do
		// nothing further.
		return
	}

	state := onLinkPrefixState{
		invalidationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			ndp.invalidateOnLinkPrefix(prefix)
		}),
	}

	if l < header.NDPInfiniteLifetime {
		state.invalidationJob.Schedule(l)
	}

	ndp.onLinkPrefixes[prefix] = state
}

// invalidateOnLinkPrefix invalidates a discovered on-link prefix.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) invalidateOnLinkPrefix(prefix tcpip.Subnet) {
	s, ok := ndp.onLinkPrefixes[prefix]

	// Is the on-link prefix still discovered?
	if !ok {
		// ...Nope, do nothing further.
		return
	}

	s.invalidationJob.Cancel()
	delete(ndp.onLinkPrefixes, prefix)

	// Let the integrator know a discovered on-link prefix is invalidated.
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnOnLinkPrefixInvalidated(ndp.ep.nic.ID(), prefix)
	}
}

// handleOnLinkPrefixInformation handles a Prefix Information option with
// its on-link flag set, as per RFC 4861 section 6.3.4.
//
// handleOnLinkPrefixInformation assumes that the prefix this pi is for is
// not the link-local prefix and the on-link flag is set.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) handleOnLinkPrefixInformation(pi header.NDPPrefixInformation) {
	prefix := pi.Subnet()
	prefixState, ok := ndp.onLinkPrefixes[prefix]
	vl := pi.ValidLifetime()

	if !ok && vl == 0 {
		// Don't know about this prefix but it has a zero valid
		// lifetime, so just ignore.
		return
	}

	if !ok && vl != 0 {
		// This is a new on-link prefix we are discovering
		//
		// Only remember it if we currently know about less than
		// MaxDiscoveredOnLinkPrefixes on-link prefixes.
		if ndp.configs.DiscoverOnLinkPrefixes && len(ndp.onLinkPrefixes) < MaxDiscoveredOnLinkPrefixes {
			ndp.rememberOnLinkPrefix(prefix, vl)
		}
		return
	}

	if ok && vl == 0 {
		// We know about the on-link prefix, but it is
		// no longer to be considered on-link, so
		// invalidate it.
		ndp.invalidateOnLinkPrefix(prefix)
		return
	}

	// This is an already discovered on-link prefix with a
	// new non-zero valid lifetime.
	//
	// Update the invalidation job.

	prefixState.invalidationJob.Cancel()

	if vl < header.NDPInfiniteLifetime {
		// Prefix is valid for a finite lifetime, schedule the job to execute after
		// the new valid lifetime.
		prefixState.invalidationJob.Schedule(vl)
	}

	ndp.onLinkPrefixes[prefix] = prefixState
}

// handleAutonomousPrefixInformation handles a Prefix Information option with
// its autonomous flag set, as per RFC 4862 section 5.5.3.
//
// handleAutonomousPrefixInformation assumes that the prefix this pi is for is
// not the link-local prefix and the autonomous flag is set.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) handleAutonomousPrefixInformation(pi header.NDPPrefixInformation) {
	vl := pi.ValidLifetime()
	pl := pi.PreferredLifetime()

	// If the preferred lifetime is greater than the valid lifetime,
	// silently ignore the Prefix Information option, as per RFC 4862
	// section 5.5.3.c.
	if pl > vl {
		return
	}

	prefix := pi.Subnet()

	// Check if we already maintain SLAAC state for prefix.
	if state, ok := ndp.slaacPrefixes[prefix]; ok {
		// As per RFC 4862 section 5.5.3.e, refresh prefix's SLAAC lifetimes.
		ndp.refreshSLAACPrefixLifetimes(prefix, &state, pl, vl)
		ndp.slaacPrefixes[prefix] = state
		return
	}

	// prefix is a new SLAAC prefix. Do the work as outlined by RFC 4862 section
	// 5.5.3.d if ndp is configured to auto-generate new addresses via SLAAC.
	if !ndp.configs.AutoGenGlobalAddresses {
		return
	}

	ndp.doSLAAC(prefix, pl, vl)
}

// doSLAAC generates a new SLAAC address with the provided lifetimes
// for prefix.
//
// pl is the new preferred lifetime. vl is the new valid lifetime.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) doSLAAC(prefix tcpip.Subnet, pl, vl time.Duration) {
	// If we do not already have an address for this prefix and the valid
	// lifetime is 0, no need to do anything further, as per RFC 4862
	// section 5.5.3.d.
	if vl == 0 {
		return
	}

	// Make sure the prefix is valid (as far as its length is concerned) to
	// generate a valid IPv6 address from an interface identifier (IID), as
	// per RFC 4862 sectiion 5.5.3.d.
	if prefix.Prefix() != validPrefixLenForAutoGen {
		return
	}

	state := slaacPrefixState{
		deprecationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			state, ok := ndp.slaacPrefixes[prefix]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry for the deprecated SLAAC prefix %s", prefix))
			}

			ndp.deprecateSLAACAddress(state.stableAddr.addressEndpoint)
		}),
		invalidationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			state, ok := ndp.slaacPrefixes[prefix]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry for the invalidated SLAAC prefix %s", prefix))
			}

			ndp.invalidateSLAACPrefix(prefix, state)
		}),
		tempAddrs:             make(map[tcpip.Address]tempSLAACAddrState),
		maxGenerationAttempts: ndp.configs.AutoGenAddressConflictRetries + 1,
	}

	now := time.Now()

	// The time an address is preferred until is needed to properly generate the
	// address.
	if pl < header.NDPInfiniteLifetime {
		state.preferredUntil = now.Add(pl)
	}

	if !ndp.generateSLAACAddr(prefix, &state) {
		// We were unable to generate an address for the prefix, we do not nothing
		// further as there is no reason to maintain state or jobs for a prefix we
		// do not have an address for.
		return
	}

	// Setup the initial jobs to deprecate and invalidate prefix.

	if pl < header.NDPInfiniteLifetime && pl != 0 {
		state.deprecationJob.Schedule(pl)
	}

	if vl < header.NDPInfiniteLifetime {
		state.invalidationJob.Schedule(vl)
		state.validUntil = now.Add(vl)
	}

	// If the address is assigned (DAD resolved), generate a temporary address.
	if state.stableAddr.addressEndpoint.GetKind() == stack.Permanent {
		// Reset the generation attempts counter as we are starting the generation
		// of a new address for the SLAAC prefix.
		ndp.generateTempSLAACAddr(prefix, &state, true /* resetGenAttempts */)
	}

	ndp.slaacPrefixes[prefix] = state
}

// addAndAcquireSLAACAddr adds a SLAAC address to the IPv6 endpoint.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) addAndAcquireSLAACAddr(addr tcpip.AddressWithPrefix, configType stack.AddressConfigType, deprecated bool) stack.AddressEndpoint {
	// Inform the integrator that we have a new SLAAC address.
	ndpDisp := ndp.ep.protocol.options.NDPDisp
	if ndpDisp == nil {
		return nil
	}

	if !ndpDisp.OnAutoGenAddress(ndp.ep.nic.ID(), addr) {
		// Informed by the integrator not to add the address.
		return nil
	}

	addressEndpoint, err := ndp.ep.addAndAcquirePermanentAddressLocked(addr, stack.FirstPrimaryEndpoint, configType, deprecated)
	if err != nil {
		panic(fmt.Sprintf("ndp: error when adding SLAAC address %+v: %s", addr, err))
	}

	return addressEndpoint
}

// generateSLAACAddr generates a SLAAC address for prefix.
//
// Returns true if an address was successfully generated.
//
// Panics if the prefix is not a SLAAC prefix or it already has an address.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) generateSLAACAddr(prefix tcpip.Subnet, state *slaacPrefixState) bool {
	if addressEndpoint := state.stableAddr.addressEndpoint; addressEndpoint != nil {
		panic(fmt.Sprintf("ndp: SLAAC prefix %s already has a permenant address %s", prefix, addressEndpoint.AddressWithPrefix()))
	}

	// If we have already reached the maximum address generation attempts for the
	// prefix, do not generate another address.
	if state.generationAttempts == state.maxGenerationAttempts {
		return false
	}

	var generatedAddr tcpip.AddressWithPrefix
	addrBytes := []byte(prefix.ID())

	for i := 0; ; i++ {
		// If we were unable to generate an address after the maximum SLAAC address
		// local regeneration attempts, do nothing further.
		if i == maxSLAACAddrLocalRegenAttempts {
			return false
		}

		dadCounter := state.generationAttempts + state.stableAddr.localGenerationFailures
		if oIID := ndp.ep.protocol.options.OpaqueIIDOpts; oIID.NICNameFromID != nil {
			addrBytes = header.AppendOpaqueInterfaceIdentifier(
				addrBytes[:header.IIDOffsetInIPv6Address],
				prefix,
				oIID.NICNameFromID(ndp.ep.nic.ID(), ndp.ep.nic.Name()),
				dadCounter,
				oIID.SecretKey,
			)
		} else if dadCounter == 0 {
			// Modified-EUI64 based IIDs have no way to resolve DAD conflicts, so if
			// the DAD counter is non-zero, we cannot use this method.
			//
			// Only attempt to generate an interface-specific IID if we have a valid
			// link address.
			//
			// TODO(b/141011931): Validate a LinkEndpoint's link address (provided by
			// LinkEndpoint.LinkAddress) before reaching this point.
			linkAddr := ndp.ep.nic.LinkAddress()
			if !header.IsValidUnicastEthernetAddress(linkAddr) {
				return false
			}

			// Generate an address within prefix from the modified EUI-64 of ndp's
			// NIC's Ethernet MAC address.
			header.EthernetAdddressToModifiedEUI64IntoBuf(linkAddr, addrBytes[header.IIDOffsetInIPv6Address:])
		} else {
			// We have no way to regenerate an address in response to an address
			// conflict when addresses are not generated with opaque IIDs.
			return false
		}

		generatedAddr = tcpip.AddressWithPrefix{
			Address:   tcpip.Address(addrBytes),
			PrefixLen: validPrefixLenForAutoGen,
		}

		if !ndp.ep.hasPermanentAddressRLocked(generatedAddr.Address) {
			break
		}

		state.stableAddr.localGenerationFailures++
	}

	if addressEndpoint := ndp.addAndAcquireSLAACAddr(generatedAddr, stack.AddressConfigSlaac, time.Since(state.preferredUntil) >= 0 /* deprecated */); addressEndpoint != nil {
		state.stableAddr.addressEndpoint = addressEndpoint
		state.generationAttempts++
		return true
	}

	return false
}

// regenerateSLAACAddr regenerates an address for a SLAAC prefix.
//
// If generating a new address for the prefix fails, the prefix is invalidated.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) regenerateSLAACAddr(prefix tcpip.Subnet) {
	state, ok := ndp.slaacPrefixes[prefix]
	if !ok {
		panic(fmt.Sprintf("ndp: SLAAC prefix state not found to regenerate address for %s", prefix))
	}

	if ndp.generateSLAACAddr(prefix, &state) {
		ndp.slaacPrefixes[prefix] = state
		return
	}

	// We were unable to generate a permanent address for the SLAAC prefix so
	// invalidate the prefix as there is no reason to maintain state for a
	// SLAAC prefix we do not have an address for.
	ndp.invalidateSLAACPrefix(prefix, state)
}

// generateTempSLAACAddr generates a new temporary SLAAC address.
//
// If resetGenAttempts is true, the prefix's generation counter is reset.
//
// Returns true if a new address was generated.
func (ndp *ndpState) generateTempSLAACAddr(prefix tcpip.Subnet, prefixState *slaacPrefixState, resetGenAttempts bool) bool {
	// Are we configured to auto-generate new temporary global addresses for the
	// prefix?
	if !ndp.configs.AutoGenTempGlobalAddresses || prefix == header.IPv6LinkLocalPrefix.Subnet() {
		return false
	}

	if resetGenAttempts {
		prefixState.generationAttempts = 0
		prefixState.maxGenerationAttempts = ndp.configs.AutoGenAddressConflictRetries + 1
	}

	// If we have already reached the maximum address generation attempts for the
	// prefix, do not generate another address.
	if prefixState.generationAttempts == prefixState.maxGenerationAttempts {
		return false
	}

	stableAddr := prefixState.stableAddr.addressEndpoint.AddressWithPrefix().Address
	now := time.Now()

	// As per RFC 4941 section 3.3 step 4, the valid lifetime of a temporary
	// address is the lower of the valid lifetime of the stable address or the
	// maximum temporary address valid lifetime.
	vl := ndp.configs.MaxTempAddrValidLifetime
	if prefixState.validUntil != (time.Time{}) {
		if prefixVL := prefixState.validUntil.Sub(now); vl > prefixVL {
			vl = prefixVL
		}
	}

	if vl <= 0 {
		// Cannot create an address without a valid lifetime.
		return false
	}

	// As per RFC 4941 section 3.3 step 4, the preferred lifetime of a temporary
	// address is the lower of the preferred lifetime of the stable address or the
	// maximum temporary address preferred lifetime - the temporary address desync
	// factor.
	pl := ndp.configs.MaxTempAddrPreferredLifetime - ndp.temporaryAddressDesyncFactor
	if prefixState.preferredUntil != (time.Time{}) {
		if prefixPL := prefixState.preferredUntil.Sub(now); pl > prefixPL {
			// Respect the preferred lifetime of the prefix, as per RFC 4941 section
			// 3.3 step 4.
			pl = prefixPL
		}
	}

	// As per RFC 4941 section 3.3 step 5, a temporary address is created only if
	// the calculated preferred lifetime is greater than the advance regeneration
	// duration. In particular, we MUST NOT create a temporary address with a zero
	// Preferred Lifetime.
	if pl <= ndp.configs.RegenAdvanceDuration {
		return false
	}

	// Attempt to generate a new address that is not already assigned to the IPv6
	// endpoint.
	var generatedAddr tcpip.AddressWithPrefix
	for i := 0; ; i++ {
		// If we were unable to generate an address after the maximum SLAAC address
		// local regeneration attempts, do nothing further.
		if i == maxSLAACAddrLocalRegenAttempts {
			return false
		}

		generatedAddr = header.GenerateTempIPv6SLAACAddr(ndp.temporaryIIDHistory[:], stableAddr)
		if !ndp.ep.hasPermanentAddressRLocked(generatedAddr.Address) {
			break
		}
	}

	// As per RFC RFC 4941 section 3.3 step 5, we MUST NOT create a temporary
	// address with a zero preferred lifetime. The checks above ensure this
	// so we know the address is not deprecated.
	addressEndpoint := ndp.addAndAcquireSLAACAddr(generatedAddr, stack.AddressConfigSlaacTemp, false /* deprecated */)
	if addressEndpoint == nil {
		return false
	}

	state := tempSLAACAddrState{
		deprecationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			prefixState, ok := ndp.slaacPrefixes[prefix]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry for %s to deprecate temporary address %s", prefix, generatedAddr))
			}

			tempAddrState, ok := prefixState.tempAddrs[generatedAddr.Address]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a tempAddr entry to deprecate temporary address %s", generatedAddr))
			}

			ndp.deprecateSLAACAddress(tempAddrState.addressEndpoint)
		}),
		invalidationJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			prefixState, ok := ndp.slaacPrefixes[prefix]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry for %s to invalidate temporary address %s", prefix, generatedAddr))
			}

			tempAddrState, ok := prefixState.tempAddrs[generatedAddr.Address]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a tempAddr entry to invalidate temporary address %s", generatedAddr))
			}

			ndp.invalidateTempSLAACAddr(prefixState.tempAddrs, generatedAddr.Address, tempAddrState)
		}),
		regenJob: ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
			prefixState, ok := ndp.slaacPrefixes[prefix]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry for %s to regenerate temporary address after %s", prefix, generatedAddr))
			}

			tempAddrState, ok := prefixState.tempAddrs[generatedAddr.Address]
			if !ok {
				panic(fmt.Sprintf("ndp: must have a tempAddr entry to regenerate temporary address after %s", generatedAddr))
			}

			// If an address has already been regenerated for this address, don't
			// regenerate another address.
			if tempAddrState.regenerated {
				return
			}

			// Reset the generation attempts counter as we are starting the generation
			// of a new address for the SLAAC prefix.
			tempAddrState.regenerated = ndp.generateTempSLAACAddr(prefix, &prefixState, true /* resetGenAttempts */)
			prefixState.tempAddrs[generatedAddr.Address] = tempAddrState
			ndp.slaacPrefixes[prefix] = prefixState
		}),
		createdAt:       now,
		addressEndpoint: addressEndpoint,
	}

	state.deprecationJob.Schedule(pl)
	state.invalidationJob.Schedule(vl)
	state.regenJob.Schedule(pl - ndp.configs.RegenAdvanceDuration)

	prefixState.generationAttempts++
	prefixState.tempAddrs[generatedAddr.Address] = state

	return true
}

// regenerateTempSLAACAddr regenerates a temporary address for a SLAAC prefix.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) regenerateTempSLAACAddr(prefix tcpip.Subnet, resetGenAttempts bool) {
	state, ok := ndp.slaacPrefixes[prefix]
	if !ok {
		panic(fmt.Sprintf("ndp: SLAAC prefix state not found to regenerate temporary address for %s", prefix))
	}

	ndp.generateTempSLAACAddr(prefix, &state, resetGenAttempts)
	ndp.slaacPrefixes[prefix] = state
}

// refreshSLAACPrefixLifetimes refreshes the lifetimes of a SLAAC prefix.
//
// pl is the new preferred lifetime. vl is the new valid lifetime.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) refreshSLAACPrefixLifetimes(prefix tcpip.Subnet, prefixState *slaacPrefixState, pl, vl time.Duration) {
	// If the preferred lifetime is zero, then the prefix should be deprecated.
	deprecated := pl == 0
	if deprecated {
		ndp.deprecateSLAACAddress(prefixState.stableAddr.addressEndpoint)
	} else {
		prefixState.stableAddr.addressEndpoint.SetDeprecated(false)
	}

	// If prefix was preferred for some finite lifetime before, cancel the
	// deprecation job so it can be reset.
	prefixState.deprecationJob.Cancel()

	now := time.Now()

	// Schedule the deprecation job if prefix has a finite preferred lifetime.
	if pl < header.NDPInfiniteLifetime {
		if !deprecated {
			prefixState.deprecationJob.Schedule(pl)
		}
		prefixState.preferredUntil = now.Add(pl)
	} else {
		prefixState.preferredUntil = time.Time{}
	}

	// As per RFC 4862 section 5.5.3.e, update the valid lifetime for prefix:
	//
	// 1) If the received Valid Lifetime is greater than 2 hours or greater than
	//    RemainingLifetime, set the valid lifetime of the prefix to the
	//    advertised Valid Lifetime.
	//
	// 2) If RemainingLifetime is less than or equal to 2 hours, ignore the
	//    advertised Valid Lifetime.
	//
	// 3) Otherwise, reset the valid lifetime of the prefix to 2 hours.

	if vl >= header.NDPInfiniteLifetime {
		// Handle the infinite valid lifetime separately as we do not schedule a
		// job in this case.
		prefixState.invalidationJob.Cancel()
		prefixState.validUntil = time.Time{}
	} else {
		var effectiveVl time.Duration
		var rl time.Duration

		// If the prefix was originally set to be valid forever, assume the
		// remaining time to be the maximum possible value.
		if prefixState.validUntil == (time.Time{}) {
			rl = header.NDPInfiniteLifetime
		} else {
			rl = time.Until(prefixState.validUntil)
		}

		if vl > MinPrefixInformationValidLifetimeForUpdate || vl > rl {
			effectiveVl = vl
		} else if rl > MinPrefixInformationValidLifetimeForUpdate {
			effectiveVl = MinPrefixInformationValidLifetimeForUpdate
		}

		if effectiveVl != 0 {
			prefixState.invalidationJob.Cancel()
			prefixState.invalidationJob.Schedule(effectiveVl)
			prefixState.validUntil = now.Add(effectiveVl)
		}
	}

	// If DAD is not yet complete on the stable address, there is no need to do
	// work with temporary addresses.
	if prefixState.stableAddr.addressEndpoint.GetKind() != stack.Permanent {
		return
	}

	// Note, we do not need to update the entries in the temporary address map
	// after updating the jobs because the jobs are held as pointers.
	var regenForAddr tcpip.Address
	allAddressesRegenerated := true
	for tempAddr, tempAddrState := range prefixState.tempAddrs {
		// As per RFC 4941 section 3.3 step 4, the valid lifetime of a temporary
		// address is the lower of the valid lifetime of the stable address or the
		// maximum temporary address valid lifetime. Note, the valid lifetime of a
		// temporary address is relative to the address's creation time.
		validUntil := tempAddrState.createdAt.Add(ndp.configs.MaxTempAddrValidLifetime)
		if prefixState.validUntil != (time.Time{}) && validUntil.Sub(prefixState.validUntil) > 0 {
			validUntil = prefixState.validUntil
		}

		// If the address is no longer valid, invalidate it immediately. Otherwise,
		// reset the invalidation job.
		newValidLifetime := validUntil.Sub(now)
		if newValidLifetime <= 0 {
			ndp.invalidateTempSLAACAddr(prefixState.tempAddrs, tempAddr, tempAddrState)
			continue
		}
		tempAddrState.invalidationJob.Cancel()
		tempAddrState.invalidationJob.Schedule(newValidLifetime)

		// As per RFC 4941 section 3.3 step 4, the preferred lifetime of a temporary
		// address is the lower of the preferred lifetime of the stable address or
		// the maximum temporary address preferred lifetime - the temporary address
		// desync factor. Note, the preferred lifetime of a temporary address is
		// relative to the address's creation time.
		preferredUntil := tempAddrState.createdAt.Add(ndp.configs.MaxTempAddrPreferredLifetime - ndp.temporaryAddressDesyncFactor)
		if prefixState.preferredUntil != (time.Time{}) && preferredUntil.Sub(prefixState.preferredUntil) > 0 {
			preferredUntil = prefixState.preferredUntil
		}

		// If the address is no longer preferred, deprecate it immediately.
		// Otherwise, schedule the deprecation job again.
		newPreferredLifetime := preferredUntil.Sub(now)
		tempAddrState.deprecationJob.Cancel()
		if newPreferredLifetime <= 0 {
			ndp.deprecateSLAACAddress(tempAddrState.addressEndpoint)
		} else {
			tempAddrState.addressEndpoint.SetDeprecated(false)
			tempAddrState.deprecationJob.Schedule(newPreferredLifetime)
		}

		tempAddrState.regenJob.Cancel()
		if tempAddrState.regenerated {
		} else {
			allAddressesRegenerated = false

			if newPreferredLifetime <= ndp.configs.RegenAdvanceDuration {
				// The new preferred lifetime is less than the advance regeneration
				// duration so regenerate an address for this temporary address
				// immediately after we finish iterating over the temporary addresses.
				regenForAddr = tempAddr
			} else {
				tempAddrState.regenJob.Schedule(newPreferredLifetime - ndp.configs.RegenAdvanceDuration)
			}
		}
	}

	// Generate a new temporary address if all of the existing temporary addresses
	// have been regenerated, or we need to immediately regenerate an address
	// due to an update in preferred lifetime.
	//
	// If each temporay address has already been regenerated, no new temporary
	// address is generated. To ensure continuation of temporary SLAAC addresses,
	// we manually try to regenerate an address here.
	if len(regenForAddr) != 0 || allAddressesRegenerated {
		// Reset the generation attempts counter as we are starting the generation
		// of a new address for the SLAAC prefix.
		if state, ok := prefixState.tempAddrs[regenForAddr]; ndp.generateTempSLAACAddr(prefix, prefixState, true /* resetGenAttempts */) && ok {
			state.regenerated = true
			prefixState.tempAddrs[regenForAddr] = state
		}
	}
}

// deprecateSLAACAddress marks the address as deprecated and notifies the NDP
// dispatcher that address has been deprecated.
//
// deprecateSLAACAddress does nothing if the address is already deprecated.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) deprecateSLAACAddress(addressEndpoint stack.AddressEndpoint) {
	if addressEndpoint.Deprecated() {
		return
	}

	addressEndpoint.SetDeprecated(true)
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnAutoGenAddressDeprecated(ndp.ep.nic.ID(), addressEndpoint.AddressWithPrefix())
	}
}

// invalidateSLAACPrefix invalidates a SLAAC prefix.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) invalidateSLAACPrefix(prefix tcpip.Subnet, state slaacPrefixState) {
	ndp.cleanupSLAACPrefixResources(prefix, state)

	if addressEndpoint := state.stableAddr.addressEndpoint; addressEndpoint != nil {
		// Since we are already invalidating the prefix, do not invalidate the
		// prefix when removing the address.
		if err := ndp.ep.removePermanentEndpointLocked(addressEndpoint, false /* allowSLAACInvalidation */); err != nil {
			panic(fmt.Sprintf("ndp: error removing stable SLAAC address %s: %s", addressEndpoint.AddressWithPrefix(), err))
		}
	}
}

// cleanupSLAACAddrResourcesAndNotify cleans up an invalidated SLAAC address's
// resources.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) cleanupSLAACAddrResourcesAndNotify(addr tcpip.AddressWithPrefix, invalidatePrefix bool) {
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnAutoGenAddressInvalidated(ndp.ep.nic.ID(), addr)
	}

	prefix := addr.Subnet()
	state, ok := ndp.slaacPrefixes[prefix]
	if !ok || state.stableAddr.addressEndpoint == nil || addr.Address != state.stableAddr.addressEndpoint.AddressWithPrefix().Address {
		return
	}

	if !invalidatePrefix {
		// If the prefix is not being invalidated, disassociate the address from the
		// prefix and do nothing further.
		state.stableAddr.addressEndpoint.DecRef()
		state.stableAddr.addressEndpoint = nil
		ndp.slaacPrefixes[prefix] = state
		return
	}

	ndp.cleanupSLAACPrefixResources(prefix, state)
}

// cleanupSLAACPrefixResources cleans up a SLAAC prefix's jobs and entry.
//
// Panics if the SLAAC prefix is not known.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) cleanupSLAACPrefixResources(prefix tcpip.Subnet, state slaacPrefixState) {
	// Invalidate all temporary addresses.
	for tempAddr, tempAddrState := range state.tempAddrs {
		ndp.invalidateTempSLAACAddr(state.tempAddrs, tempAddr, tempAddrState)
	}

	if state.stableAddr.addressEndpoint != nil {
		state.stableAddr.addressEndpoint.DecRef()
		state.stableAddr.addressEndpoint = nil
	}
	state.deprecationJob.Cancel()
	state.invalidationJob.Cancel()
	delete(ndp.slaacPrefixes, prefix)
}

// invalidateTempSLAACAddr invalidates a temporary SLAAC address.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) invalidateTempSLAACAddr(tempAddrs map[tcpip.Address]tempSLAACAddrState, tempAddr tcpip.Address, tempAddrState tempSLAACAddrState) {
	// Since we are already invalidating the address, do not invalidate the
	// address when removing the address.
	if err := ndp.ep.removePermanentEndpointLocked(tempAddrState.addressEndpoint, false /* allowSLAACInvalidation */); err != nil {
		panic(fmt.Sprintf("error removing temporary SLAAC address %s: %s", tempAddrState.addressEndpoint.AddressWithPrefix(), err))
	}

	ndp.cleanupTempSLAACAddrResources(tempAddrs, tempAddr, tempAddrState)
}

// cleanupTempSLAACAddrResourcesAndNotify cleans up an invalidated temporary
// SLAAC address's resources from ndp.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) cleanupTempSLAACAddrResourcesAndNotify(addr tcpip.AddressWithPrefix, invalidateAddr bool) {
	if ndpDisp := ndp.ep.protocol.options.NDPDisp; ndpDisp != nil {
		ndpDisp.OnAutoGenAddressInvalidated(ndp.ep.nic.ID(), addr)
	}

	if !invalidateAddr {
		return
	}

	prefix := addr.Subnet()
	state, ok := ndp.slaacPrefixes[prefix]
	if !ok {
		panic(fmt.Sprintf("ndp: must have a slaacPrefixes entry to clean up temp addr %s resources", addr))
	}

	tempAddrState, ok := state.tempAddrs[addr.Address]
	if !ok {
		panic(fmt.Sprintf("ndp: must have a tempAddr entry to clean up temp addr %s resources", addr))
	}

	ndp.cleanupTempSLAACAddrResources(state.tempAddrs, addr.Address, tempAddrState)
}

// cleanupTempSLAACAddrResourcesAndNotify cleans up a temporary SLAAC address's
// jobs and entry.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) cleanupTempSLAACAddrResources(tempAddrs map[tcpip.Address]tempSLAACAddrState, tempAddr tcpip.Address, tempAddrState tempSLAACAddrState) {
	tempAddrState.addressEndpoint.DecRef()
	tempAddrState.addressEndpoint = nil
	tempAddrState.deprecationJob.Cancel()
	tempAddrState.invalidationJob.Cancel()
	tempAddrState.regenJob.Cancel()
	delete(tempAddrs, tempAddr)
}

// removeSLAACAddresses removes all SLAAC addresses.
//
// If keepLinkLocal is false, the SLAAC generated link-local address is removed.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) removeSLAACAddresses(keepLinkLocal bool) {
	linkLocalSubnet := header.IPv6LinkLocalPrefix.Subnet()
	var linkLocalPrefixes int
	for prefix, state := range ndp.slaacPrefixes {
		// RFC 4862 section 5 states that routers are also expected to generate a
		// link-local address so we do not invalidate them if we are cleaning up
		// host-only state.
		if keepLinkLocal && prefix == linkLocalSubnet {
			linkLocalPrefixes++
			continue
		}

		ndp.invalidateSLAACPrefix(prefix, state)
	}

	if got := len(ndp.slaacPrefixes); got != linkLocalPrefixes {
		panic(fmt.Sprintf("ndp: still have non-linklocal SLAAC prefixes after cleaning up; found = %d prefixes, of which %d are link-local", got, linkLocalPrefixes))
	}
}

// cleanupState cleans up ndp's state.
//
// If hostOnly is true, then only host-specific state is cleaned up.
//
// This function invalidates all discovered on-link prefixes, discovered
// routers, and auto-generated addresses.
//
// If hostOnly is true, then the link-local auto-generated address aren't
// invalidated as routers are also expected to generate a link-local address.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) cleanupState(hostOnly bool) {
	ndp.removeSLAACAddresses(hostOnly /* keepLinkLocal */)

	for prefix := range ndp.onLinkPrefixes {
		ndp.invalidateOnLinkPrefix(prefix)
	}

	if got := len(ndp.onLinkPrefixes); got != 0 {
		panic(fmt.Sprintf("ndp: still have discovered on-link prefixes after cleaning up; found = %d", got))
	}

	for router := range ndp.defaultRouters {
		ndp.invalidateDefaultRouter(router)
	}

	if got := len(ndp.defaultRouters); got != 0 {
		panic(fmt.Sprintf("ndp: still have discovered default routers after cleaning up; found = %d", got))
	}

	ndp.dhcpv6Configuration = 0
}

// startSolicitingRouters starts soliciting routers, as per RFC 4861 section
// 6.3.7. If routers are already being solicited, this function does nothing.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) startSolicitingRouters() {
	if ndp.rtrSolicitJob != nil {
		// We are already soliciting routers.
		return
	}

	remaining := ndp.configs.MaxRtrSolicitations
	if remaining == 0 {
		return
	}

	// Calculate the random delay before sending our first RS, as per RFC
	// 4861 section 6.3.7.
	var delay time.Duration
	if ndp.configs.MaxRtrSolicitationDelay > 0 {
		delay = time.Duration(rand.Int63n(int64(ndp.configs.MaxRtrSolicitationDelay)))
	}

	ndp.rtrSolicitJob = ndp.ep.protocol.stack.NewJob(&ndp.ep.mu, func() {
		// As per RFC 4861 section 4.1, the source of the RS is an address assigned
		// to the sending interface, or the unspecified address if no address is
		// assigned to the sending interface.
		localAddr := header.IPv6Any
		if addressEndpoint := ndp.ep.acquireOutgoingPrimaryAddressRLocked(header.IPv6AllRoutersMulticastAddress, false); addressEndpoint != nil {
			localAddr = addressEndpoint.AddressWithPrefix().Address
			addressEndpoint.DecRef()
		}

		// As per RFC 4861 section 4.1, an NDP RS SHOULD include the source
		// link-layer address option if the source address of the NDP RS is
		// specified. This option MUST NOT be included if the source address is
		// unspecified.
		//
		// TODO(b/141011931): Validate a LinkEndpoint's link address (provided by
		// LinkEndpoint.LinkAddress) before reaching this point.
		var optsSerializer header.NDPOptionsSerializer
		linkAddress := ndp.ep.nic.LinkAddress()
		if localAddr != header.IPv6Any && header.IsValidUnicastEthernetAddress(linkAddress) {
			optsSerializer = header.NDPOptionsSerializer{
				header.NDPSourceLinkLayerAddressOption(linkAddress),
			}
		}
		payloadSize := header.ICMPv6HeaderSize + header.NDPRSMinimumSize + int(optsSerializer.Length())
		icmpData := header.ICMPv6(buffer.NewView(payloadSize))
		icmpData.SetType(header.ICMPv6RouterSolicit)
		rs := header.NDPRouterSolicit(icmpData.MessageBody())
		rs.Options().Serialize(optsSerializer)
		icmpData.SetChecksum(header.ICMPv6Checksum(icmpData, localAddr, header.IPv6AllRoutersMulticastAddress, buffer.VectorisedView{}))

		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: int(ndp.ep.MaxHeaderLength()),
			Data:               buffer.View(icmpData).ToVectorisedView(),
		})

		sent := ndp.ep.protocol.stack.Stats().ICMP.V6.PacketsSent
		ndp.ep.addIPHeader(localAddr, header.IPv6AllRoutersMulticastAddress, pkt, stack.NetworkHeaderParams{
			Protocol: header.ICMPv6ProtocolNumber,
			TTL:      header.NDPHopLimit,
		}, nil /* extensionHeaders */)

		if err := ndp.ep.nic.WritePacketToRemote(header.EthernetAddressFromMulticastIPv6Address(header.IPv6AllRoutersMulticastAddress), nil /* gso */, ProtocolNumber, pkt); err != nil {
			sent.Dropped.Increment()
			log.Printf("startSolicitingRouters: error writing NDP router solicit message on NIC(%d); err = %s", ndp.ep.nic.ID(), err)
			// Don't send any more messages if we had an error.
			remaining = 0
		} else {
			sent.RouterSolicit.Increment()
			remaining--
		}

		if remaining != 0 {
			ndp.rtrSolicitJob.Schedule(ndp.configs.RtrSolicitationInterval)
		}
	})

	ndp.rtrSolicitJob.Schedule(delay)
}

// stopSolicitingRouters stops soliciting routers. If routers are not currently
// being solicited, this function does nothing.
//
// The IPv6 endpoint that ndp belongs to MUST be locked.
func (ndp *ndpState) stopSolicitingRouters() {
	if ndp.rtrSolicitJob == nil {
		// Nothing to do.
		return
	}

	ndp.rtrSolicitJob.Cancel()
	ndp.rtrSolicitJob = nil
}

// initializeTempAddrState initializes state related to temporary SLAAC
// addresses.
func (ndp *ndpState) initializeTempAddrState() {
	header.InitialTempIID(ndp.temporaryIIDHistory[:], ndp.ep.protocol.options.TempIIDSeed, ndp.ep.nic.ID())

	if MaxDesyncFactor != 0 {
		ndp.temporaryAddressDesyncFactor = time.Duration(rand.Int63n(int64(MaxDesyncFactor)))
	}
}
