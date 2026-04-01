# frozen_string_literal: true

require "tmpdir"

RSpec.describe Git::Treeline::Registry do
  let(:tmpdir) { Dir.mktmpdir }
  let(:registry_path) { File.join(tmpdir, "registry.json") }
  let(:registry) { described_class.new(registry_path) }

  after { FileUtils.rm_rf(tmpdir) }

  let(:sample_entry) do
    {
      project: "myapp",
      worktree: "/tmp/myapp-feature",
      worktree_name: "feature",
      port: 3010,
      database: "myapp_dev_feature",
      redis_prefix: "myapp:feature",
      redis_db: nil
    }
  end

  describe "#allocate" do
    it "writes an entry to the registry" do
      registry.allocate(sample_entry)

      expect(registry.allocations.size).to eq(1)
      expect(registry.allocations.first["project"]).to eq("myapp")
      expect(registry.allocations.first["port"]).to eq(3010)
    end

    it "adds allocated_at timestamp" do
      registry.allocate(sample_entry)

      expect(registry.allocations.first["allocated_at"]).not_to be_nil
    end

    it "replaces existing allocation for same worktree path" do
      registry.allocate(sample_entry)
      registry.allocate(sample_entry.merge(port: 3020))

      expect(registry.allocations.size).to eq(1)
      expect(registry.allocations.first["port"]).to eq(3020)
    end

    it "persists to disk as JSON" do
      registry.allocate(sample_entry)

      data = JSON.parse(File.read(registry_path))
      expect(data["version"]).to eq(1)
      expect(data["allocations"].size).to eq(1)
    end
  end

  describe "#find" do
    it "returns the matching allocation" do
      registry.allocate(sample_entry)

      result = registry.find("/tmp/myapp-feature")
      expect(result["port"]).to eq(3010)
    end

    it "returns nil for unknown path" do
      expect(registry.find("/nonexistent")).to be_nil
    end
  end

  describe "#find_by_project" do
    it "returns all allocations for a project" do
      registry.allocate(sample_entry)
      registry.allocate(sample_entry.merge(worktree: "/tmp/myapp-bugfix", worktree_name: "bugfix", port: 3020))

      results = registry.find_by_project("myapp")
      expect(results.size).to eq(2)
    end

    it "returns empty array for unknown project" do
      expect(registry.find_by_project("unknown")).to eq([])
    end
  end

  describe "#release" do
    it "removes the allocation" do
      registry.allocate(sample_entry)
      registry.release("/tmp/myapp-feature")

      expect(registry.allocations).to be_empty
    end

    it "returns true when an allocation was removed" do
      registry.allocate(sample_entry)
      expect(registry.release("/tmp/myapp-feature")).to be true
    end

    it "returns false when nothing was removed" do
      expect(registry.release("/nonexistent")).to be false
    end
  end

  describe "#used_ports" do
    it "returns all allocated ports" do
      registry.allocate(sample_entry)
      registry.allocate(sample_entry.merge(worktree: "/tmp/other", port: 3020))

      expect(registry.used_ports).to contain_exactly(3010, 3020)
    end

    it "returns all ports from multi-port allocations" do
      registry.allocate(sample_entry.merge(ports: [3010, 3011]))
      registry.allocate(sample_entry.merge(worktree: "/tmp/other", port: 3020, ports: [3020, 3021]))

      expect(registry.used_ports).to contain_exactly(3010, 3011, 3020, 3021)
    end

    it "handles mix of old single-port and new multi-port entries" do
      registry.allocate(sample_entry) # old format: port only, no ports array
      registry.allocate(sample_entry.merge(worktree: "/tmp/other", port: 3020, ports: [3020, 3021]))

      expect(registry.used_ports).to contain_exactly(3010, 3020, 3021)
    end
  end

  describe "#prune" do
    it "removes allocations for non-existent directories" do
      registry.allocate(sample_entry.merge(worktree: "/nonexistent/path"))

      count = registry.prune
      expect(count).to eq(1)
      expect(registry.allocations).to be_empty
    end

    it "keeps allocations for directories that exist" do
      registry.allocate(sample_entry.merge(worktree: tmpdir))

      count = registry.prune
      expect(count).to eq(0)
      expect(registry.allocations.size).to eq(1)
    end
  end

  describe "file locking" do
    it "creates a lock file during writes" do
      lock_path = "#{registry_path}.lock"
      registry.allocate(sample_entry)

      expect(File.exist?(lock_path)).to be true
    end
  end

  describe "corrupt registry" do
    it "recovers from invalid JSON" do
      File.write(registry_path, "not json {{{")

      expect(registry.allocations).to eq([])
    end
  end
end
