# frozen_string_literal: true

require "tmpdir"

RSpec.describe Git::Treeline::Allocator do
  let(:tmpdir) { Dir.mktmpdir }
  let(:registry) { Git::Treeline::Registry.new(File.join(tmpdir, "registry.json")) }

  let(:user_config) do
    Git::Treeline::UserConfig.new(File.join(tmpdir, "config.json"))
  end

  let(:project_root) { tmpdir }

  let(:project_config) do
    File.write(File.join(project_root, ".treeline.yml"), <<~YAML)
      project: testapp
      database:
        adapter: postgresql
        template: testapp_dev
        pattern: "{template}_{worktree}"
    YAML
    Git::Treeline::ProjectConfig.new(project_root)
  end

  let(:allocator) do
    described_class.new(user_config: user_config, project_config: project_config, registry: registry)
  end

  after { FileUtils.rm_rf(tmpdir) }

  describe "#next_available_ports" do
    it "starts at base + increment for a single port" do
      expect(allocator.next_available_ports(1)).to eq([3010])
    end

    it "returns contiguous block for multiple ports" do
      expect(allocator.next_available_ports(3)).to eq([3010, 3011, 3012])
    end

    it "skips blocks where any port is in use" do
      registry.allocate(project: "other", worktree: "/tmp/a", worktree_name: "a",
                        port: 3011, ports: [3011], database: nil, redis_prefix: nil, redis_db: nil)

      expect(allocator.next_available_ports(3)).to eq([3020, 3021, 3022])
    end

    it "skips multiple used blocks" do
      registry.allocate(project: "x", worktree: "/tmp/a", worktree_name: "a",
                        port: 3010, ports: [3010, 3011], database: nil, redis_prefix: nil, redis_db: nil)
      registry.allocate(project: "x", worktree: "/tmp/b", worktree_name: "b",
                        port: 3020, ports: [3020, 3021], database: nil, redis_prefix: nil, redis_db: nil)

      expect(allocator.next_available_ports(2)).to eq([3030, 3031])
    end
  end

  describe "#allocate" do
    it "returns a complete allocation hash with ports array" do
      result = allocator.allocate(worktree_path: "/tmp/feature", worktree_name: "feature")

      expect(result[:project]).to eq("testapp")
      expect(result[:port]).to eq(3010)
      expect(result[:ports]).to eq([3010])
      expect(result[:port_1]).to eq(3010)
      expect(result[:database]).to eq("testapp_dev_feature")
      expect(result[:worktree]).to eq("/tmp/feature")
      expect(result[:worktree_name]).to eq("feature")
    end

    it "defaults to prefixed redis strategy" do
      result = allocator.allocate(worktree_path: "/tmp/feature", worktree_name: "feature")

      expect(result[:redis_prefix]).to eq("testapp:feature")
      expect(result[:redis_db]).to be_nil
    end

    context "with multi-port project" do
      let(:project_config) do
        File.write(File.join(project_root, ".treeline.yml"), <<~YAML)
          project: testapp
          ports_needed: 2
          database:
            adapter: postgresql
            template: testapp_dev
            pattern: "{template}_{worktree}"
        YAML
        Git::Treeline::ProjectConfig.new(project_root)
      end

      it "allocates contiguous port block" do
        result = allocator.allocate(worktree_path: "/tmp/feature", worktree_name: "feature")

        expect(result[:port]).to eq(3010)
        expect(result[:ports]).to eq([3010, 3011])
        expect(result[:port_1]).to eq(3010)
        expect(result[:port_2]).to eq(3011)
      end

      it "does not overlap with other allocations" do
        registry.allocate(project: "other", worktree: "/tmp/a", worktree_name: "a",
                          port: 3010, ports: [3010, 3011], database: nil, redis_prefix: nil, redis_db: nil)

        result = allocator.allocate(worktree_path: "/tmp/feature", worktree_name: "feature")

        expect(result[:ports]).to eq([3020, 3021])
        expect(result[:ports] & [3010, 3011]).to be_empty
      end
    end

    context "when ports_needed exceeds port_increment" do
      let(:project_config) do
        File.write(File.join(project_root, ".treeline.yml"), <<~YAML)
          project: testapp
          ports_needed: 15
        YAML
        Git::Treeline::ProjectConfig.new(project_root)
      end

      it "raises an error" do
        expect do
          allocator.allocate(worktree_path: "/tmp/feature", worktree_name: "feature")
        end.to raise_error(Git::Treeline::Error, /ports_needed.*exceeds.*port\.increment/)
      end
    end
  end

  describe "#build_database_name" do
    it "interpolates template and worktree name" do
      expect(allocator.build_database_name("feature-x")).to eq("testapp_dev_feature_x")
    end

    it "sanitizes special characters in worktree names" do
      expect(allocator.build_database_name("feat/some-branch")).to eq("testapp_dev_feat_some_branch")
    end

    it "returns nil when no template configured" do
      File.write(File.join(project_root, ".treeline.yml"), <<~YAML)
        project: testapp
        database:
          adapter: postgresql
      YAML
      config = Git::Treeline::ProjectConfig.new(project_root)
      alloc = described_class.new(user_config: user_config, project_config: config, registry: registry)

      expect(alloc.build_database_name("feature")).to be_nil
    end
  end

  describe "#allocate_redis" do
    context "with prefixed strategy" do
      it "returns a namespace prefix" do
        result = allocator.allocate_redis("feature")

        expect(result[:redis_prefix]).to eq("testapp:feature")
        expect(result[:redis_db]).to be_nil
      end
    end

    context "with database strategy" do
      let(:user_config) do
        path = File.join(tmpdir, "config.json")
        File.write(path,
                   JSON.generate("port" => { "base" => 3000, "increment" => 10 },
                                 "redis" => { "strategy" => "database",
                                              "url" => "redis://localhost:6379" }))
        Git::Treeline::UserConfig.new(path)
      end

      it "returns a redis DB number" do
        result = allocator.allocate_redis("feature")

        expect(result[:redis_db]).to eq(1)
        expect(result[:redis_prefix]).to be_nil
      end
    end
  end

  describe "#build_redis_url" do
    it "returns base URL for prefixed strategy" do
      alloc = { redis_db: nil, redis_prefix: "myapp:feature" }
      expect(allocator.build_redis_url(alloc)).to eq("redis://localhost:6379")
    end

    it "appends DB number for database strategy" do
      alloc = { redis_db: 3, redis_prefix: nil }
      expect(allocator.build_redis_url(alloc)).to eq("redis://localhost:6379/3")
    end
  end
end
