#!/usr/bin/env ruby
# frozen_string_literal: true

# tools/set-testflight-notes.rb
#
# Push the current CHANGELOG.md version section into TestFlight's
# "What to Test" notes via the App Store Connect API.
#
# altool uploads the binary but cannot set test notes — those live on
# the betaBuildLocalizations resource, which only exists once the build
# has been uploaded and registered (a few minutes after altool returns).
# So this runs as a SEPARATE step after upload, polls until the build
# appears, then sets the en-US whatsNew on every matching build (iOS +
# Mac Catalyst share a version but are distinct Build resources).
#
# Pure Ruby stdlib — no gems. ES256 JWT is minted with OpenSSL and the
# DER signature converted to the JOSE raw r||s form by hand.
#
# Usage:
#   set-testflight-notes.rb \
#     --key-id KEY --issuer ISSUER --p8 PATH \
#     --bundle-id net.phfactor.Mapbook \
#     --version 0.4.0 --build 123 \
#     --changelog CHANGELOG.md
#
# Env fallbacks: APP_STORE_KEY_ID, APP_STORE_ISSUER_ID, APP_STORE_P8.

require 'openssl'
require 'json'
require 'base64'
require 'net/http'
require 'uri'
require 'optparse'

API_HOST = 'api.appstoreconnect.apple.com'
POLL_INTERVAL = 30   # seconds between build-lookup retries
POLL_TIMEOUT  = 1500 # give up after 25 min
MAX_NOTES = 4000     # App Store Connect's whatsNew limit

# ---------------------------------------------------------------------
# Options
# ---------------------------------------------------------------------
opts = {
  key_id: ENV['APP_STORE_KEY_ID'],
  issuer: ENV['APP_STORE_ISSUER_ID'],
  p8: ENV['APP_STORE_P8'],
  changelog: 'CHANGELOG.md',
  min_builds: 1
}
OptionParser.new do |o|
  o.on('--key-id ID')    { |v| opts[:key_id] = v }
  o.on('--issuer ID')    { |v| opts[:issuer] = v }
  o.on('--p8 PATH')      { |v| opts[:p8] = v }
  o.on('--bundle-id ID') { |v| opts[:bundle_id] = v }
  o.on('--version V')    { |v| opts[:version] = v }
  o.on('--build N')      { |v| opts[:build] = v }
  o.on('--changelog P')  { |v| opts[:changelog] = v }
  # iOS + Mac Catalyst register as separate builds and process at
  # different speeds (the .pkg is slower). Wait for this many before
  # setting notes, so we don't miss the sibling. Default 1.
  o.on('--min-builds N') { |v| opts[:min_builds] = v.to_i }
  # Comma-separated beta-group names to distribute each build to after
  # the notes are set. External groups additionally get a Beta App
  # Review submission (required for the first build of a version;
  # already-reviewed builds fail that POST harmlessly). This is what
  # removes the "add the TestFlight group to every release by hand"
  # chore — internal groups are better served by the one-time
  # "automatic distribution" toggle in the TestFlight UI.
  o.on('--groups NAMES') { |v| opts[:groups] = v.split(',').map(&:strip).reject(&:empty?) }
  # Print the app's beta groups (id, internal/external, auto-notify)
  # and exit. Handy for discovering the exact names --groups expects.
  o.on('--list-groups') { opts[:list_groups] = true }
end.parse!

# --list-groups only needs auth + app lookup; version/build are for
# the notes/distribution path.
required = opts[:list_groups] ? %i[key_id issuer bundle_id]
                              : %i[key_id issuer bundle_id version build]
required.each do |k|
  abort "error: missing --#{k.to_s.tr('_', '-')}" if opts[k].nil? || opts[k].to_s.empty?
end
# Default p8 path follows Apple's convention.
opts[:p8] ||= File.expand_path("~/.appstoreconnect/private_keys/AuthKey_#{opts[:key_id]}.p8")
abort "error: p8 key not found at #{opts[:p8]}" unless File.exist?(opts[:p8])

# ---------------------------------------------------------------------
# JWT (ES256)
# ---------------------------------------------------------------------
def b64url(bytes)
  Base64.urlsafe_encode64(bytes).delete('=')
end

# OpenSSL EC#sign emits a DER SEQUENCE { INTEGER r, INTEGER s }. JOSE
# wants the fixed-width raw concatenation r||s (32 bytes each for P-256).
def der_to_jose(der)
  seq = OpenSSL::ASN1.decode(der)
  r = seq.value[0].value.to_s(2)
  s = seq.value[1].value.to_s(2)
  r.rjust(32, "\x00") + s.rjust(32, "\x00")
end

def mint_jwt(key_id:, issuer:, p8_path:)
  now = Time.now.to_i
  header = { alg: 'ES256', kid: key_id, typ: 'JWT' }
  payload = {
    iss: issuer,
    iat: now,
    exp: now + 1200, # max 20 min
    aud: 'appstoreconnect-v1'
  }
  signing_input = "#{b64url(JSON.dump(header))}.#{b64url(JSON.dump(payload))}"
  key = OpenSSL::PKey.read(File.read(p8_path))
  der = key.sign(OpenSSL::Digest.new('SHA256'), signing_input)
  "#{signing_input}.#{b64url(der_to_jose(der))}"
end

# ---------------------------------------------------------------------
# ASC API helpers
# ---------------------------------------------------------------------
# allow_failure: return nil on a non-2xx instead of aborting — for
# calls that legitimately fail in normal operation (e.g. re-submitting
# an already-reviewed build for beta review).
def api(method, path, token, body: nil, allow_failure: false)
  uri = URI("https://#{API_HOST}#{path}")
  http = Net::HTTP.new(uri.host, uri.port)
  http.use_ssl = true
  req = case method
        when :get then Net::HTTP::Get.new(uri)
        when :post then Net::HTTP::Post.new(uri)
        when :patch then Net::HTTP::Patch.new(uri)
        else abort "bad method #{method}"
        end
  req['Authorization'] = "Bearer #{token}"
  req['Content-Type'] = 'application/json'
  req.body = JSON.dump(body) if body
  res = http.request(req)
  unless res.code.to_i.between?(200, 299)
    return nil if allow_failure
    abort "error: #{method.upcase} #{path} -> #{res.code}\n#{res.body}"
  end
  # A 204 No Content (e.g. a successful relationships POST) has a nil
  # body — treat nil like empty so callers get {} for "success, no
  # payload" rather than a NoMethodError.
  res.body.nil? || res.body.empty? ? {} : JSON.parse(res.body)
end

def find_app_id(token, bundle_id)
  data = api(:get, "/v1/apps?filter[bundleId]=#{URI.encode_www_form_component(bundle_id)}", token)['data']
  abort "error: no app with bundleId #{bundle_id}" if data.nil? || data.empty?
  data.first['id']
end

# Poll until at least `min_builds` builds matching version + build-number
# show up. Returns an array of build ids (iOS + Mac Catalyst are separate
# builds). If the timeout hits with at least one build found, we proceed
# with what we have rather than failing — partial notes beat none.
def wait_for_builds(token, app_id, version, build, min_builds)
  path = "/v1/builds?filter[app]=#{app_id}" \
         "&filter[preReleaseVersion.version]=#{URI.encode_www_form_component(version)}" \
         "&filter[version]=#{URI.encode_www_form_component(build)}" \
         '&limit=10'
  waited = 0
  loop do
    data = api(:get, path, token)['data'] || []
    return data.map { |b| b['id'] } if data.length >= min_builds
    if waited >= POLL_TIMEOUT
      abort "error: build #{version} (#{build}) never appeared after #{POLL_TIMEOUT}s" if data.empty?
      warn "warning: only #{data.length}/#{min_builds} builds registered after #{POLL_TIMEOUT}s; " \
           'setting notes on what\'s there. Re-run later to catch the rest.'
      return data.map { |b| b['id'] }
    end
    puts "  …#{data.length}/#{min_builds} builds registered, retrying in #{POLL_INTERVAL}s (#{waited}s elapsed)"
    sleep POLL_INTERVAL
    waited += POLL_INTERVAL
  end
end

def set_notes_for_build(token, build_id, notes)
  locs = api(:get, "/v1/builds/#{build_id}/betaBuildLocalizations", token)['data'] || []
  en = locs.find { |l| l.dig('attributes', 'locale') == 'en-US' }
  if en
    api(:patch, "/v1/betaBuildLocalizations/#{en['id']}", token, body: {
          data: { type: 'betaBuildLocalizations', id: en['id'],
                  attributes: { whatsNew: notes } }
        })
    puts "  ✓ updated notes on build #{build_id}"
  else
    api(:post, '/v1/betaBuildLocalizations', token, body: {
          data: { type: 'betaBuildLocalizations',
                  attributes: { locale: 'en-US', whatsNew: notes },
                  relationships: { build: { data: { type: 'builds', id: build_id } } } }
        })
    puts "  ✓ created en-US notes on build #{build_id}"
  end
end

# ---------------------------------------------------------------------
# Beta-group distribution
# ---------------------------------------------------------------------
def fetch_beta_groups(token, app_id)
  api(:get, "/v1/apps/#{app_id}/betaGroups?limit=200", token)['data'] || []
end

def print_beta_groups(groups)
  if groups.empty?
    puts '  (no beta groups)'
    return
  end
  puts 'Beta groups:'
  groups.each do |g|
    a = g['attributes'] || {}
    kind = a['isInternalGroup'] ? 'internal' : 'external'
    auto = a['hasAccessToAllBuilds'] ? ', auto-gets-every-build' : ''
    puts "  • #{a['name']}  [#{kind}#{auto}]"
  end
end

# Resolve requested group names (case-insensitive) to betaGroup objects,
# aborting with the list of available names if any miss.
def resolve_groups(token, app_id, names)
  available = fetch_beta_groups(token, app_id)
  by_name = available.each_with_object({}) do |g, h|
    h[g.dig('attributes', 'name').to_s.downcase] = g
  end
  names.map do |n|
    g = by_name[n.downcase]
    unless g
      abort "error: no beta group named #{n.inspect}. Available: " \
            "#{available.map { |x| x.dig('attributes', 'name') }.join(', ')}"
    end
    g
  end
end

# Attach every build to the named groups, and — if any is external —
# submit each build for Beta App Review (required before external
# testers can install). This is the piece that removes the manual
# "add the TestFlight group to every release" step.
def distribute_to_groups(token, app_id, build_ids, names)
  groups = resolve_groups(token, app_id, names)
  group_ids = groups.map { |g| g['id'] }
  group_names = groups.map { |g| g.dig('attributes', 'name') }.join(', ')
  has_external = groups.any? { |g| !g.dig('attributes', 'isInternalGroup') }
  build_ids.each do |bid|
    # allow_failure: distribution is additive and best-effort — the
    # notes (the critical path) already landed. A 403 here means the
    # API key's role is too low (needs App Manager / Admin), not a bug;
    # warn loudly with the fix rather than aborting mid-release.
    res = api(:post, "/v1/builds/#{bid}/relationships/betaGroups", token, allow_failure: true,
              body: { data: group_ids.map { |gid| { type: 'betaGroups', id: gid } } })
    if res.nil?
      warn "  ⚠ couldn't distribute build #{bid} to #{group_names}."
      warn '    Most likely the App Store Connect API key lacks TestFlight'
      warn '    management rights. In ASC → Users and Access → Integrations,'
      warn '    the key needs the App Manager (or Admin) role — a Developer-'
      warn '    role key can set notes but not manage beta groups. Generate a'
      warn '    new key at that role, drop the .p8 in ~/.appstoreconnect/'
      warn '    private_keys/, point APP_STORE_KEY_ID at it, and re-run.'
      next
    end
    puts "  ✓ distributed build #{bid} → #{group_names}"
    # External distribution needs Beta App Review. The first build of a
    # version requires it; re-submitting an already-reviewed build 409s,
    # which is harmless — allow_failure so re-runs stay green.
    next unless has_external

    ok = api(:post, '/v1/betaAppReviewSubmissions', token, allow_failure: true,
             body: { data: { type: 'betaAppReviewSubmissions',
                             relationships: { build: { data: { type: 'builds', id: bid } } } } })
    puts(ok ? "  ✓ submitted build #{bid} for beta review"
            : "  · build #{bid} already submitted/approved for beta review")
  end
end

# ---------------------------------------------------------------------
# Changelog → plain-text notes
# ---------------------------------------------------------------------
# Extract the body under `## [VERSION]` up to the next `## [` heading.
def changelog_section(path, version)
  # Force UTF-8 — the changelog has em dashes, bullets and smart
  # quotes, and Ruby's default external encoding may be US-ASCII.
  text = File.read(path, encoding: 'UTF-8')
  start = text =~ /^## \[#{Regexp.escape(version)}\][^\n]*\n/
  abort "error: no [#{version}] section in #{path}" if start.nil?
  body_start = start + Regexp.last_match(0).length
  rest = text[body_start..]
  nxt = rest =~ /^## \[/
  (nxt ? rest[0...nxt] : rest).strip
end

# TestFlight renders plain text — markdown shows literally. Strip it.
def demarkdown(md)
  md.lines.map do |line|
    l = line.rstrip
    l = l.sub(/^\s*[-*]\s+/, '• ')                 # bullets
    l = l.gsub(/\*\*(.+?)\*\*/, '\1')              # bold
    l = l.gsub(/`([^`]+)`/, '\1')                  # inline code
    l = l.gsub(/\[([^\]]+)\]\(([^)]+)\)/, '\1 (\2)') # links → text (url)
    l
  end.join("\n").gsub(/\n{3,}/, "\n\n").strip
end

# ---------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------
token = mint_jwt(key_id: opts[:key_id], issuer: opts[:issuer], p8_path: opts[:p8])
app_id = find_app_id(token, opts[:bundle_id])
puts "  app id: #{app_id}"

# --list-groups is a standalone query — print and bail before any of
# the build/notes machinery.
if opts[:list_groups]
  print_beta_groups(fetch_beta_groups(token, app_id))
  exit 0
end

notes = demarkdown(changelog_section(opts[:changelog], opts[:version]))
if notes.length > MAX_NOTES
  notes = "#{notes[0, MAX_NOTES - 1]}…"
  warn "warning: notes truncated to #{MAX_NOTES} chars"
end

puts "Setting TestFlight notes for #{opts[:version]} (build #{opts[:build]})…"
build_ids = wait_for_builds(token, app_id, opts[:version], opts[:build], opts[:min_builds])
puts "  found #{build_ids.length} build(s): #{build_ids.join(', ')}"
# Tokens expire in 20 min; the poll can eat into that. Re-mint to be safe.
token = mint_jwt(key_id: opts[:key_id], issuer: opts[:issuer], p8_path: opts[:p8])
build_ids.each { |id| set_notes_for_build(token, id, notes) }

distribute_to_groups(token, app_id, build_ids, opts[:groups]) if opts[:groups]&.any?

tail = opts[:groups]&.any? ? 'Notes + group distribution' : 'Notes'
puts "Done. #{tail} live in App Store Connect → TestFlight."
